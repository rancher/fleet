package deployer

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/rancher/fleet/internal/bundlereader"
	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/internal/helmdeployer"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/internal/ocistorage"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/condition"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// deployErrPattern matches Helm/Kubernetes error messages that should be
// recorded as status conditions rather than returned as reconciler errors.
var deployErrPattern = regexp.MustCompile(
	"(timed out waiting for the condition)|" + // a Helm wait occurs and it times out
		"(error validating data)|" + // manifests fail to pass validation (client-side OpenAPI schema)
		"(chart requires kubeVersion)|" + // kubeVersion mismatch
		"(annotation validation error)|" + // annotations fail to pass validation
		"(failed, and has been rolled back due to atomic being set)|" + // atomic is set and a rollback occurs
		"(YAML parse error)|" + // YAML is broken in source files (Helm v3)
		"(MalformedYAMLError)|" + // YAML is broken in source files (Helm v4)
		"(unknown field)|" + // unknown field rejected by the API server (e.g. via server-side apply strict validation)
		"(Forbidden: updates to [0-9A-Za-z]+ spec for fields other than [0-9A-Za-z ']+ are forbidden)|" + // trying to update fields that cannot be updated
		"(Forbidden: spec is immutable after creation)|" + // trying to modify immutable spec
		"(chart requires kubeVersion: [0-9A-Za-z\\.\\-<>=]+ which is incompatible with Kubernetes)", // trying to deploy to incompatible Kubernetes
)

type NotReadyDependenciesError struct {
	Pending []string
}

func (e *NotReadyDependenciesError) Error() string {
	return fmt.Sprintf("dependent bundle(s) are not ready: %v", e.Pending)
}

// NamespaceForbiddenError indicates the deployment's service account is not
// allowed to mutate the target namespace. The Helm release installed, but the
// requested namespaceLabels/namespaceAnnotations could not be applied, so the
// deployment is reported not-ready. It is handled as a controlled requeue
// rather than a failed reconcile (to avoid tight-looping); the patch converges
// once the missing namespace RBAC is granted. That grant is not a watched
// resource and would never re-trigger a reconcile on its own, hence the
// controlled requeue. It unwraps to the underlying Forbidden error, so
// apierrors.IsForbidden still reports true.
type NamespaceForbiddenError struct {
	err error
}

func (e *NamespaceForbiddenError) Error() string { return e.err.Error() }

func (e *NamespaceForbiddenError) Unwrap() error { return e.err }

type Deployer struct {
	client         client.Client
	upstreamClient client.Reader
	lookup         Lookup
	helm           *helmdeployer.Helm
}

type Lookup interface {
	Get(ctx context.Context, client client.Reader, id string) (*manifest.Manifest, error)
}

func New(localClient client.Client, upstreamClient client.Reader, lookup Lookup, deployer *helmdeployer.Helm) *Deployer {
	return &Deployer{
		client:         localClient,
		upstreamClient: upstreamClient,
		lookup:         lookup,
		helm:           deployer,
	}
}

func (d *Deployer) Resources(name string, releaseID string) (*helmdeployer.Resources, error) {
	return d.helm.Resources(name, releaseID)
}

func (d *Deployer) RemoveExternalChanges(ctx context.Context, bd *fleet.BundleDeployment) (string, error) {
	return d.helm.RemoveExternalChanges(ctx, bd)
}

// DeployBundle deploys the bundle deployment with the helm SDK. It does not
// mutate bd, instead it returns the modified status
// If force is true, bd will be upgraded even if its contents have not changed; this is useful for
// applying changes coming from external resources, such as those referenced through valuesFrom.
func (d *Deployer) DeployBundle(
	ctx context.Context,
	bd *fleet.BundleDeployment,
	force bool,
) (fleet.BundleDeploymentStatus, error) {
	status := bd.Status
	logger := log.FromContext(ctx).WithName("deploy-bundle").WithValues("deploymentID", bd.Spec.DeploymentID, "appliedDeploymentID", status.AppliedDeploymentID)

	if err := d.checkDependency(ctx, bd); err != nil {
		logger.V(1).Info("Bundle has a dependency that is not ready", "error", err)
		return status, err
	}

	releaseID, err := d.helmdeploy(ctx, logger, bd, force)

	if err != nil {
		// When an error from DeployBundle is returned it causes DeployBundle
		// to requeue and keep trying to deploy on a loop. If there is something
		// wrong with the deployed manifests this will be a loop that re-deploying
		// cannot fix. Here we catch those errors and update the status to note
		// the problem while skipping the constant requeuing.
		if do, newStatus := deployErrToStatus(err, status); do {
			// Setting the release to an empty string removes the previous
			// release name. When a deployment fails the release name is not
			// returned. Keeping the old release name can lead to other functions
			// looking up old data in the history and presenting the wrong status.
			// For example, the deployManager.Deploy function will find the old
			// release and not return an error. It will set everything as if the
			// current one is running properly.
			newStatus.Release = ""
			newStatus.AppliedDeploymentID = bd.Spec.DeploymentID
			return newStatus, nil
		}
		return status, err
	}
	status.Release = releaseID
	status.AppliedDeploymentID = bd.Spec.DeploymentID

	if err := d.setNamespaceLabelsAndAnnotations(ctx, bd, releaseID); err != nil {
		// A permission error here means the deployment's service account is
		// not allowed to mutate the target namespace. Record it on the status
		// and return a typed error so the controller does a controlled requeue
		// (the missing namespace RBAC is not watched, so granting it would not
		// otherwise re-trigger a reconcile) rather than tight-looping.
		if do, newStatus := forbiddenToStatus(err, status); do {
			newStatus.Release = releaseID
			newStatus.AppliedDeploymentID = bd.Spec.DeploymentID
			return newStatus, &NamespaceForbiddenError{err: err}
		}
		return fleet.BundleDeploymentStatus{}, err
	}

	// Setting the error to nil clears any existing error
	condition.Cond(fleet.BundleDeploymentConditionInstalled).SetError(&status, "", nil)
	return status, nil
}

// Deploy the bundle deployment, i.e. with helmdeployer.
// This loads the manifest and the contents from the upstream cluster.
// If force is true, checks on whether the bundle deployment exists will be skipped, leading to the bundle deployment
// being updated even if its deployment ID has not changed.
func (d *Deployer) helmdeploy(ctx context.Context, logger logr.Logger, bd *fleet.BundleDeployment, force bool) (string, error) {
	if !force && bd.Spec.DeploymentID == bd.Status.AppliedDeploymentID {
		if ok, err := d.helm.EnsureInstalled(bd.Name, bd.Status.Release); err != nil {
			return "", err
		} else if ok {
			return bd.Status.Release, nil
		}
	}

	// manifestID is used for manifest/OCI lookups.
	// DeploymentID format is "manifestID:optionsHash".
	// When only options change (e.g., adding comparePatches for drift acceptance),
	// the optionsHash changes but the manifestID remains the same. This allows
	// options-only changes to be deployed without re-fetching the manifest.
	manifestID := bd.Spec.DeploymentID
	if specManifestID, _, found := strings.Cut(bd.Spec.DeploymentID, ":"); found {
		manifestID = specManifestID
	}

	var (
		m   *manifest.Manifest
		err error
	)
	switch {
	case bd.Spec.OCIContents:
		oci := ocistorage.NewOCIWrapper()
		secretID := client.ObjectKey{Name: manifestID, Namespace: bd.Namespace}
		opts, err := ocistorage.ReadOptsFromSecret(ctx, d.upstreamClient, secretID)
		if err != nil {
			return "", err
		}
		m, err = oci.PullManifest(ctx, opts, manifestID)
		if err != nil {
			return "", err
		}
		// Verify that the calculated manifestID for the manifest
		// we just downloaded matches the expected one.
		// Otherwise, the manifest will be considered incorrect or corrupted.
		actualID, err := m.ID()
		if err != nil {
			return "", err
		}
		if actualID != manifestID {
			return "", fmt.Errorf("invalid or corrupt manifest. Expecting id: %q, got %q", manifestID, actualID)
		}
	case bd.Spec.HelmChartOptions != nil:
		m, err = bundlereader.GetManifestFromHelmChart(ctx, d.upstreamClient, bd)
		if err != nil {
			return "", err
		}
	default:
		m, err = d.lookup.Get(ctx, d.upstreamClient, manifestID)
		if err != nil {
			return "", err
		}
	}

	m.Commit = bd.Labels[fleet.CommitLabel]
	release, err := d.helm.Deploy(ctx, bd.Name, m, bd.Spec.Options)
	if err != nil {
		return "", err
	}

	resourceID := helmdeployer.ReleaseToResourceID(release)

	logger.Info("Deployed bundle", "release", resourceID, "DeploymentID", bd.Spec.DeploymentID)

	return resourceID, nil
}

// setNamespaceLabelsAndAnnotations updates the namespace for the release, applying all labels and annotations to that namespace as configured in the bundle spec.
func (d *Deployer) setNamespaceLabelsAndAnnotations(ctx context.Context, bd *fleet.BundleDeployment, releaseID string) error {
	if bd.Spec.Options.NamespaceLabels == nil && bd.Spec.Options.NamespaceAnnotations == nil {
		return nil
	}

	// Patch the namespace as the deployment's service account so that this
	// operation is gated by the same downstream RBAC as the deployment itself,
	// rather than by the agent's cluster-admin credentials. When the deployment
	// resolves to no service account, fall back to the agent client, preserving
	// the previous behaviour.
	c, err := d.namespaceClient(ctx, bd)
	if err != nil {
		return err
	}

	ns, err := fetchNamespace(ctx, c, releaseID)
	if err != nil {
		return err
	}

	desiredLabels := maps.Clone(ns.Labels)
	if bd.Spec.Options.NamespaceLabels != nil {
		if desiredLabels == nil {
			desiredLabels = make(map[string]string)
		}
		addLabelsFromOptions(log.FromContext(ctx), desiredLabels, bd.Spec.Options.NamespaceLabels)
	}
	desiredAnnotations := maps.Clone(ns.Annotations)
	if bd.Spec.Options.NamespaceAnnotations != nil {
		if desiredAnnotations == nil {
			desiredAnnotations = make(map[string]string)
		}
		addAnnotationsFromOptions(desiredAnnotations, bd.Spec.Options.NamespaceAnnotations)
	}

	if maps.Equal(desiredLabels, ns.Labels) && maps.Equal(desiredAnnotations, ns.Annotations) {
		return nil
	}

	ns.Labels = desiredLabels
	ns.Annotations = desiredAnnotations
	return updateNamespace(ctx, c, ns)
}

// namespaceClient returns the client to use for namespace label/annotation
// mutations. When the deployment resolves to a service account (pinned, or the
// "fleet-default" fallback), it returns a client impersonating that account, so
// the mutation is authorized against the downstream RBAC of the tenant rather
// than the agent's cluster-admin credentials. Otherwise it returns the agent
// client, preserving the previous behaviour.
func (d *Deployer) namespaceClient(ctx context.Context, bd *fleet.BundleDeployment) (client.Client, error) {
	if d.helm != nil {
		c, err := d.helm.ImpersonatedClient(ctx, bd.Spec.Options.ServiceAccount)
		if err != nil {
			return nil, err
		}
		if c != nil {
			return c, nil
		}
	}
	return d.client, nil
}

// updateNamespace updates a namespace resource in the cluster.
func updateNamespace(ctx context.Context, c client.Client, ns *corev1.Namespace) error {
	if err := c.Update(ctx, ns); err != nil {
		if apierrors.IsForbidden(err) {
			return fmt.Errorf("the deployment's service account is not allowed to update namespace %q; "+
				"grant it 'update' on this namespace (scopeable via resourceNames) or remove "+
				"namespaceLabels/namespaceAnnotations: %w", ns.Name, err)
		}
		return err
	}

	return nil
}

// fetchNamespace gets the namespace matching the release ID. Returns an error if none is found.
// releaseID is composed of release.Namespace/release.Name/release.Version
func fetchNamespace(ctx context.Context, c client.Client, releaseID string) (*corev1.Namespace, error) {
	namespace := strings.Split(releaseID, "/")[0]
	ns := &corev1.Namespace{}
	if err := c.Get(ctx, types.NamespacedName{Name: namespace}, ns); err != nil {
		if apierrors.IsForbidden(err) {
			return nil, fmt.Errorf("the deployment's service account is not allowed to get namespace %q; "+
				"grant it 'get' on this namespace (scopeable via resourceNames) or remove "+
				"namespaceLabels/namespaceAnnotations: %w", namespace, err)
		}
		return nil, err
	}
	return ns, nil
}

const podSecurityLabelPrefix = "pod-security.kubernetes.io/"

// addLabelsFromOptions updates nsLabels to contain labels from optLabels, while preserving
// the `kubernetes.io/metadata.name` label added by Kubernetes when creating the namespace
// and any existing `pod-security.kubernetes.io/*` labels. Labels with the
// `pod-security.kubernetes.io/` prefix in optLabels are ignored.
//
// This filtering is intentionally unconditional and independent of the
// service-account impersonation used for the namespace patch: it must also hold
// for deployments that run as the agent (no service account pinned), where
// there is no downstream RBAC gating at all. It is the only safeguard against a
// bundle escalating pod-security enforcement on its target namespace. To set
// pod-security labels on a namespace, declare them on the Namespace resource in
// the bundle instead.
func addLabelsFromOptions(logger logr.Logger, nsLabels map[string]string, optLabels map[string]string) {
	for k, v := range optLabels {
		if strings.HasPrefix(k, podSecurityLabelPrefix) {
			logger.V(1).Info("Ignoring label from options", "label", k)
			continue
		}
		nsLabels[k] = v
	}

	// Delete labels not defined in the options.
	// Keep the `kubernetes.io/metadata.name` label as it is added by kubernetes when creating the namespace.
	// Keep pod-security.kubernetes.io/ labels as they are managed by cluster administrators.
	for k := range nsLabels {
		if strings.HasPrefix(k, podSecurityLabelPrefix) {
			continue
		}
		if _, ok := optLabels[k]; k != corev1.LabelMetadataName && !ok {
			delete(nsLabels, k)
		}
	}
}

// addAnnotationsFromOptions updates nsAnnotations so that it only contains all annotations specified in optAnnotations.
func addAnnotationsFromOptions(nsAnnotations map[string]string, optAnnotations map[string]string) {
	maps.Copy(nsAnnotations, optAnnotations)

	// Delete Annotations not defined in the options.
	for k := range nsAnnotations {
		if _, ok := optAnnotations[k]; !ok {
			delete(nsAnnotations, k)
		}
	}
}

// deployErrToStatus converts an error into a status update
func deployErrToStatus(err error, status fleet.BundleDeploymentStatus) (bool, fleet.BundleDeploymentStatus) {
	if err == nil {
		return false, status
	}

	msg := err.Error()

	// The following error conditions are turned into a status
	// Note: these error strings are returned by the Helm SDK and its dependencies
	if deployErrPattern.MatchString(msg) {
		status.Ready = false
		status.NonModified = true

		// The ready status is displayed throughout the UI. Setting this as well
		// as installed enables the status to be displayed when looking at the
		// CRD or a UI build on that.
		readyError := fmt.Errorf("not ready: %s", msg)
		condition.Cond(fleet.BundleDeploymentConditionReady).SetError(&status, "", readyError)

		// Deployed and Monitored conditions are handled in the reconciler.
		// They are true if the deployer returns no error and false if
		// an error is returned. When an error is returned they are
		// requeued. To capture the state of an error that is not
		// returned a new condition is being captured. Ready is the
		// condition that displays for status in general and it is used
		// for the readiness of resources. Only when we cannot capture
		// the status of resources, like here, can use it for a
		// message like the above. The Installed condition lets us have
		// a condition to capture the error that can be bubbled up for
		// Bundles and Gitrepos to consume.
		installError := fmt.Errorf("not installed: %s", msg)
		condition.Cond(fleet.BundleDeploymentConditionInstalled).SetError(&status, "", installError)

		return true, status
	}

	// The case that the bundle is already in an error state. A previous
	// condition with the error should already be applied.
	if errors.Is(err, helmdeployer.ErrNoResourceID) {
		return true, status
	}

	return false, status
}

// forbiddenToStatus records a namespace permission error as a status condition,
// mirroring deployErrToStatus. Such errors occur when the deployment's service
// account is not allowed to mutate the target namespace. The condition surfaces
// the missing RBAC on the bundle deployment; the caller wraps the error in a
// NamespaceForbiddenError so the controller does a controlled requeue (at
// NamespacePermissionRequeueInterval) rather than tight-looping, letting the
// patch converge once the permission is granted.
func forbiddenToStatus(err error, status fleet.BundleDeploymentStatus) (bool, fleet.BundleDeploymentStatus) {
	if !apierrors.IsForbidden(err) {
		return false, status
	}

	status.Ready = false
	status.NonModified = true

	msg := err.Error()
	condition.Cond(fleet.BundleDeploymentConditionReady).SetError(&status, "", fmt.Errorf("not ready: %s", msg))
	condition.Cond(fleet.BundleDeploymentConditionInstalled).SetError(&status, "", fmt.Errorf("not installed: %s", msg))

	return true, status
}

func (d *Deployer) checkDependency(ctx context.Context, bd *fleet.BundleDeployment) error {
	var depBundleList []string
	bundleNamespace := bd.Labels[fleet.BundleNamespaceLabel]
	for _, depend := range bd.Spec.DependsOn {
		// skip empty BundleRef definitions. Possible if there is a typo in the yaml
		if depend.Name != "" || depend.Selector != nil {
			ls := &metav1.LabelSelector{}
			if depend.Selector != nil {
				ls = depend.Selector
			}

			// depend.Name is just a shortcut for matchLabels: {bundle-name: name}
			if depend.Name != "" {
				ls = metav1.AddLabelToSelector(ls, fleet.BundleLabel, depend.Name)
				ls = metav1.AddLabelToSelector(ls, fleet.BundleNamespaceLabel, bundleNamespace)
			}

			selector, err := metav1.LabelSelectorAsSelector(ls)
			if err != nil {
				return err
			}

			bds := fleet.BundleDeploymentList{}
			err = d.upstreamClient.List(ctx, &bds, client.MatchingLabelsSelector{Selector: selector}, client.InNamespace(bd.Namespace))
			if err != nil {
				return err
			}

			if len(bds.Items) == 0 {
				return fmt.Errorf("list bundledeployments: no bundles matching labels %s in namespace %s", selector.String(), bundleNamespace)
			}

			for _, depBundle := range bds.Items {
				if !isDependencyReady(depBundle, depend.AcceptedStates) {
					depBundleList = append(depBundleList, depBundle.Name)
				}

			}
		}
	}

	if len(depBundleList) != 0 {
		return &NotReadyDependenciesError{Pending: depBundleList}
	}

	return nil
}

// isStateAccepted checks if currentState is in acceptedStates.
// If acceptedStates is empty or nil, only Ready is accepted (default behavior).
func isStateAccepted(currentState fleet.BundleState, acceptedStates []fleet.BundleState) bool {
	if len(acceptedStates) == 0 {
		return currentState == fleet.Ready
	}
	return slices.Contains(acceptedStates, currentState)
}

// isDependencyReady checks if a BundleDeployment dependency is in an acceptable state.
// acceptedStates is a list of states that are considered acceptable for this dependency.
// If acceptedStates is empty or nil, only the "Ready" state is accepted (default behavior).
func isDependencyReady(depBundle fleet.BundleDeployment, acceptedStates []fleet.BundleState) bool {
	return isStateAccepted(summary.GetDeploymentState(&depBundle), acceptedStates)
}
