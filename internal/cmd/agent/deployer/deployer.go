package deployer

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/rancher/fleet/internal/bundlereader"
	"github.com/rancher/fleet/internal/helmdeployer"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/internal/ocistorage"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/rancher/wrangler/v3/pkg/kv"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

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
	manifestID, _ := kv.Split(bd.Spec.DeploymentID, ":")
	var (
		m   *manifest.Manifest
		err error
	)
	if bd.Spec.OCIContents {
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
	} else if bd.Spec.HelmChartOptions != nil {
		m, err = bundlereader.GetManifestFromHelmChart(ctx, d.upstreamClient, bd)
		if err != nil {
			return "", err
		}
	} else {
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

	ns, err := d.fetchNamespace(ctx, releaseID)
	if err != nil {
		return err
	}

	if reflect.DeepEqual(bd.Spec.Options.NamespaceLabels, ns.Labels) && reflect.DeepEqual(bd.Spec.Options.NamespaceAnnotations, ns.Annotations) {
		return nil
	}

	if bd.Spec.Options.NamespaceLabels != nil {
		addLabelsFromOptions(ns.Labels, bd.Spec.Options.NamespaceLabels)
	}
	if bd.Spec.Options.NamespaceAnnotations != nil {
		if ns.Annotations == nil {
			ns.Annotations = map[string]string{}
		}
		addAnnotationsFromOptions(ns.Annotations, bd.Spec.Options.NamespaceAnnotations)
	}
	err = d.updateNamespace(ctx, ns)
	if err != nil {
		return err
	}

	return nil
}

// updateNamespace updates a namespace resource in the cluster.
func (d *Deployer) updateNamespace(ctx context.Context, ns *corev1.Namespace) error {
	err := d.client.Update(ctx, ns)
	if err != nil {
		return err
	}

	return nil
}

// fetchNamespace gets the namespace matching the release ID. Returns an error if none is found.
// releaseID is composed of release.Namespace/release.Name/release.Version
func (d *Deployer) fetchNamespace(ctx context.Context, releaseID string) (*corev1.Namespace, error) {
	namespace := strings.Split(releaseID, "/")[0]
	ns := &corev1.Namespace{}
	err := d.client.Get(ctx, types.NamespacedName{Name: namespace}, ns)
	if err != nil {
		return nil, err
	}
	return ns, nil
}

// addLabelsFromOptions updates nsLabels so that it only contains all labels specified in optLabels, plus the `kubernetes.io/metadata.name` labels added by kubernetes when creating the namespace.
func addLabelsFromOptions(nsLabels map[string]string, optLabels map[string]string) {
	for k, v := range optLabels {
		nsLabels[k] = v
	}

	// Delete labels not defined in the options.
	// Keep the `kubernetes.io/metadata.name` label as it is added by kubernetes when creating the namespace.
	for k := range nsLabels {
		if _, ok := optLabels[k]; k != corev1.LabelMetadataName && !ok {
			delete(nsLabels, k)
		}
	}
}

// addAnnotationsFromOptions updates nsAnnotations so that it only contains all annotations specified in optAnnotations.
func addAnnotationsFromOptions(nsAnnotations map[string]string, optAnnotations map[string]string) {
	for k, v := range optAnnotations {
		nsAnnotations[k] = v
	}

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
	re := regexp.MustCompile(
		"(timed out waiting for the condition)|" + // a Helm wait occurs and it times out
			"(error validating data)|" + // manifests fail to pass validation
			"(chart requires kubeVersion)|" + // kubeVersion mismatch
			"(annotation validation error)|" + // annotations fail to pass validation
			"(failed, and has been rolled back due to atomic being set)|" + // atomic is set and a rollback occurs
			"(YAML parse error)|" + // YAML is broken in source files
			"(Forbidden: updates to [0-9A-Za-z]+ spec for fields other than [0-9A-Za-z ']+ are forbidden)|" + // trying to update fields that cannot be updated
			"(Forbidden: spec is immutable after creation)|" + // trying to modify immutable spec
			"(chart requires kubeVersion: [0-9A-Za-z\\.\\-<>=]+ which is incompatible with Kubernetes)", // trying to deploy to incompatible Kubernetes
	)
	if re.MatchString(msg) {
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
		// the status of resources, like here, can use use it for a
		// message like the above. The Installed condition lets us have
		// a condition to capture the error that can be bubbled up for
		// Bundles and Gitrepos to consume.
		installError := fmt.Errorf("not installed: %s", msg)
		condition.Cond(fleet.BundleDeploymentConditionInstalled).SetError(&status, "", installError)

		return true, status
	}

	// The case that the bundle is already in an error state. A previous
	// condition with the error should already be applied.
	if err == helmdeployer.ErrNoResourceID {
		return true, status
	}

	return false, status
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
				c := condition.Cond("Ready")
				if c.IsTrue(depBundle) {
					continue
				} else {
					depBundleList = append(depBundleList, depBundle.Name)
				}
			}
		}
	}

	if len(depBundleList) != 0 {
		return fmt.Errorf("dependent bundle(s) are not ready: %v", depBundleList)
	}

	return nil
}
