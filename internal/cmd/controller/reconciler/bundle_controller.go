// Copyright (c) 2021-2023 SUSE LLC

package reconciler

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/go-logr/logr"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/kv"
	fleetutil "github.com/rancher/fleet/internal/cmd/controller/errorutil"
	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/experimental"
	"github.com/rancher/fleet/internal/helmvalues"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/internal/metrics"
	"github.com/rancher/fleet/internal/ocistorage"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	fleetevent "github.com/rancher/fleet/pkg/event"
	"github.com/rancher/fleet/pkg/sharding"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// period after which the Bundle reconciler is re-scheduled, in order to wait for the BundleDeploymentReconciler cleanup to finish
	requeueAfterBundleDeploymentCleanup = 2 * time.Second
)

type BundleQuery interface {
	// BundlesForCluster is used to map from a cluster to bundles
	BundlesForCluster(context.Context, *fleet.Cluster) ([]*fleet.Bundle, []*fleet.Bundle, error)
}

type Store interface {
	Store(context.Context, *manifest.Manifest) error
}

type TargetBuilder interface {
	Targets(ctx context.Context, bundle *fleet.Bundle, manifestID string) ([]*target.Target, error)
}

// BundleReconciler reconciles a Bundle object
type BundleReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	Builder TargetBuilder
	Store   Store
	Query   BundleQuery
	ShardID string

	Workers int
}

// SetupWithManager sets up the controller with the Manager.
func (r *BundleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.Bundle{},
			builder.WithPredicates(
				// do not trigger for bundle status changes (except for cache sync)
				predicate.Or(
					TypedResourceVersionUnchangedPredicate[client.Object]{},
					predicate.GenerationChangedPredicate{},
					predicate.AnnotationChangedPredicate{},
					predicate.LabelChangedPredicate{},
				),
			),
		).
		// Note: Maybe improve with WatchesMetadata, does it have access to labels?
		Watches(
			// Fan out from bundledeployment to bundle, this is useful to update the
			// bundle's status fields.
			&fleet.BundleDeployment{}, handler.EnqueueRequestsFromMapFunc(BundleDeploymentMapFunc(r)),
			builder.WithPredicates(bundleDeploymentStatusChangedPredicate()),
		).
		Watches(
			// Fan out from cluster to bundle, this is useful for targeting and templating.
			&fleet.Cluster{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, a client.Object) []ctrl.Request {
				cluster := a.(*fleet.Cluster)
				bundlesToRefresh, _, err := r.Query.BundlesForCluster(ctx, cluster)
				if err != nil {
					return nil
				}
				requests := []ctrl.Request{}
				for _, bundle := range bundlesToRefresh {
					if !sharding.ShouldProcess(bundle, r.ShardID) {
						continue
					}
					requests = append(requests, ctrl.Request{
						NamespacedName: types.NamespacedName{
							Namespace: bundle.Namespace,
							Name:      bundle.Name,
						},
					})
				}

				return requests
			}),
			builder.WithPredicates(clusterChangedPredicate()),
		).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)
}

//+kubebuilder:rbac:groups=fleet.cattle.io,resources=bundles,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=bundles/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=bundles/finalizers,verbs=update

// Reconcile creates bundle deployments for a bundle
func (r *BundleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("bundle")
	ctx = log.IntoContext(ctx, logger)

	bundle := &fleet.Bundle{}
	if err := r.Get(ctx, req.NamespacedName, bundle); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if bundle.Labels[fleet.RepoLabel] != "" {
		logger = logger.WithValues(
			"gitrepo", bundle.Labels[fleet.RepoLabel],
			"commit", bundle.Labels[fleet.CommitLabel],
		)
	}

	if userID := bundle.Labels[fleet.CreatedByUserIDLabel]; userID != "" {
		logger = logger.WithValues("userID", userID)
	}

	if !bundle.DeletionTimestamp.IsZero() {
		return r.handleDelete(ctx, logger, req, bundle)
	}

	bundleOrig := bundle.DeepCopy()

	if err := r.ensureFinalizer(ctx, bundle); err != nil {
		// Retry without updating the status, as this should be a transient error about which users can't do
		// anything.
		return ctrl.Result{}, fmt.Errorf("%w, failed to add finalizer to bundle: %w", fleetutil.ErrRetryable, err)
	}

	// Migration: Remove the obsolete created-by-display-name label if it exists
	if err := r.removeDisplayNameLabel(ctx, bundle); err != nil {
		return r.computeResult(ctx, logger, bundleOrig, bundle, "failed to remove display name label", err)
	}

	logger.V(1).Info(
		"Reconciling bundle, checking targets, calculating changes, building objects",
		"generation",
		bundle.Generation,
		"observedGeneration",
		bundle.Status.ObservedGeneration,
	)

	// The values secret is optional, e.g. for non-helm type bundles.
	// This sets the values on the bundle, which is safe as we don't update bundle, just its status
	if bundle.Spec.ValuesHash != "" {
		if err := loadBundleValues(ctx, r.Client, bundle); err != nil {
			return r.computeResult(ctx, logger, bundleOrig, bundle, "failed to load values secret for bundle", err)
		}
	}

	contentsInOCI := bundle.Spec.ContentsID != "" && ocistorage.OCIIsEnabled()
	contentsInHelmChart := bundle.Spec.HelmOpOptions != nil

	// Skip bundle deployment creation if the bundle is a HelmOps bundle and the configured Helm version is still a
	// version constraint. That constraint should be resolved into a strict version by the HelmOps reconciler before bundle
	// deployments can be created.
	if contentsInHelmChart && bundle.Spec.Helm != nil && len(bundle.Spec.Helm.Version) > 0 {
		// #3953
		// There are helm repositories that list chart versions with the "v" prefix.
		// That's not recommended by Helm as the version with the prefix is not semver compliant,
		// but those repositories are still valid.
		// Delete the "v" prefix (if found) before checking for a valid semver.
		versionToCheck := strings.TrimPrefix(bundle.Spec.Helm.Version, "v")
		if _, err := semver.StrictNewVersion(versionToCheck); err != nil {
			err = fmt.Errorf("chart version cannot be deployed; check HelmOp status for more details: %w", err)

			return ctrl.Result{}, r.updateErrorStatus(ctx, bundleOrig, bundle, err)
		}
	}

	manifestID := bundle.Spec.ContentsID
	var resourcesManifest *manifest.Manifest
	if !contentsInOCI && !contentsInHelmChart {
		resourcesManifest = manifest.FromBundle(bundle)
		if bundle.Generation != bundle.Status.ObservedGeneration {
			resourcesManifest.ResetSHASum()
		}

		manifestDigest, err := resourcesManifest.SHASum()
		if err != nil {
			return ctrl.Result{},
				r.updateErrorStatus(
					ctx,
					bundleOrig,
					bundle,
					fmt.Errorf("failed to compute resources manifest SHA sum for OCI storage: %w", err),
				)
		}
		bundle.Status.ResourcesSHA256Sum = manifestDigest

		manifestID, err = resourcesManifest.ID()
		if err != nil {
			// this should never happen, since manifest.SHASum() cached the result and worked above.
			return ctrl.Result{},
				r.updateErrorStatus(
					ctx,
					bundleOrig,
					bundle,
					fmt.Errorf("failed to compute resources manifest ID for OCI storage: %w", err),
				)
		}
	}

	matchedTargets, err := r.Builder.Targets(ctx, bundle, manifestID)
	if err != nil {
		return ctrl.Result{},
			r.updateErrorStatus(
				ctx,
				bundleOrig,
				bundle,
				fmt.Errorf("targeting error: %w", err),
			)
	}

	if (!contentsInOCI && !contentsInHelmChart) && len(matchedTargets) > 0 {
		// when not using the OCI registry or helm chart we need to create a contents resource
		// so the BundleDeployments are able to access the contents to be deployed.
		// Otherwise, do not create a content resource if there are no targets.
		// `fleet apply` puts all resources into `bundle.Spec.Resources`.
		// `Store` copies all the resources into the content resource.
		// There is no pruning of unused resources. Therefore we write
		// the content resource immediately, even though
		// `BundleDeploymentOptions`, e.g. `targetCustomizations` on
		// the `helm.Chart` field, change which resources are used. The
		// agents have access to all resources and use their specific
		// set of `BundleDeploymentOptions`.
		if err := r.Store.Store(ctx, resourcesManifest); err != nil {
			return r.computeResult(ctx, logger, bundleOrig, bundle, "could not copy manifest into Content resource", err)
		}
	}
	logger = logger.WithValues("manifestID", manifestID)

	if err := resetStatus(&bundle.Status, matchedTargets); err != nil {
		err = fmt.Errorf("failed to reset bundle status from targets: %w", err)

		return ctrl.Result{}, r.updateErrorStatus(ctx, bundleOrig, bundle, err)
	}

	// this will add the defaults for a new bundledeployment. It propagates stagedOptions to options.
	if err := target.UpdatePartitions(&bundle.Status, matchedTargets); err != nil {
		err = fmt.Errorf("failed to update partitions: %w", err)

		return ctrl.Result{}, r.updateErrorStatus(ctx, bundleOrig, bundle, err)
	}

	if contentsInOCI {
		url, err := r.getOCIReference(ctx, bundle)
		if err != nil {
			return r.computeResult(ctx, logger, bundleOrig, bundle, "failed to build OCI reference", err)
		}
		bundle.Status.OCIReference = url
	}

	// ResourceKey is deprecated and no longer used by the UI.
	bundle.Status.ResourceKey = nil

	summary.SetReadyConditions(&bundle.Status, "Cluster", bundle.Status.Summary)
	bundle.Status.ObservedGeneration = bundle.Generation

	// build BundleDeployments out of targets discarding Status, replacing DependsOn with the
	// bundle's DependsOn (pure function) and replacing the labels with the bundle's labels
	merr := []error{}
	bundleDeploymentUIDs := make(sets.Set[types.UID])
	for _, target := range matchedTargets {
		if target.Deployment == nil {
			continue
		}
		if target.Deployment.Namespace == "" {
			logger.V(1).Info(
				"Skipping bundledeployment with empty namespace, waiting for agentmanagement to set cluster.status.namespace",
				"bundledeployment", target.Deployment,
			)
			continue
		}

		// NOTE we don't re-use the existing BundleDeployment, we discard annotations, status, etc.
		// and copy labels from Bundle as they might have changed.
		// However, matchedTargets target.Deployment contains existing BundleDeployments.
		bd := target.BundleDeployment()
		logger := logger.WithValues("bundledeployment", bd.Name)

		// No need to check the deletion timestamp here before adding a finalizer, since the bundle has just
		// been created.
		controllerutil.AddFinalizer(bd, finalize.BundleDeploymentFinalizer)

		bd.Spec.OCIContents = contentsInOCI
		bd.Spec.HelmChartOptions = bundle.Spec.HelmOpOptions

		h, options, stagedOptions, err := helmvalues.ExtractOptions(bd)
		if err != nil {
			err := fmt.Errorf("failed to extract Helm options for secret creation: %w", err)

			return ctrl.Result{}, r.updateErrorStatus(ctx, bundleOrig, bundle, err)
		}
		// We need a checksum to trigger on value change, rely on later code in
		// the reconciler to update the status
		bd.Spec.ValuesHash = h

		helmvalues.ClearOptions(bd)

		bd, err = r.createBundleDeployment(
			ctx,
			logger,
			bd,
			contentsInOCI,
			bundle.Spec.HelmOpOptions != nil,
			manifestID)
		if err != nil {
			// We could end up here, because we cannot add a
			// finalizer to a content resource, which has a
			// deletion timestamp.
			// Log the problem and keep trying to create the other
			// bundledeployments, but retry the whole reconcile
			// afterwards.
			merr = append(merr, fmt.Errorf("failed to create bundle deployment: %w", err))
			logger.Info(fmt.Sprintf("failed to create a bundledeployment, skipping and requeuing: %v", err))
			continue
		}
		bundleDeploymentUIDs.Insert(bd.UID)

		if bd.Spec.ValuesHash != "" {
			if err := r.createOptionsSecret(ctx, bd, options, stagedOptions); err != nil {
				return r.computeResult(ctx, logger, bundleOrig, bundle, "failed to create options secret", err)
			}
		} else {
			// No values to store, delete the secret if it exists
			if err := r.Delete(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: bd.Name, Namespace: bd.Namespace},
			}); err != nil && !apierrors.IsNotFound(err) {
				err = fmt.Errorf("%w: failed to delete options secret: %w", fleetutil.ErrRetryable, err)
				logger.Info(err.Error())

				return ctrl.Result{RequeueAfter: durations.DefaultRequeueAfter}, nil
			}
		}

		if err := r.handleDownstreamObjects(ctx, bundle, bd); err != nil {
			return r.computeResult(ctx, logger, bundleOrig, bundle, "failed to clone config maps and secrets downstream", err)
		}

		if err := r.handleContentAccessSecrets(ctx, bundle, bd); err != nil {
			return r.computeResult(ctx, logger, bundleOrig, bundle, "failed to clone secrets downstream", err)
		}
	}

	// the targets configuration may have changed, leaving behind some BundleDeployments that are no longer needed
	if err := r.cleanupOrphanedBundleDeployments(ctx, bundle, bundleDeploymentUIDs); err != nil {
		logger.V(1).Error(err, "deleting orphaned bundle deployments", "bundle", bundle.GetName())
	}

	updateDisplay(&bundle.Status)
	if err := r.updateStatus(ctx, bundleOrig, bundle); err != nil {
		merr = append(merr, err)
		return ctrl.Result{}, errutil.NewAggregate(merr)
	}

	return ctrl.Result{}, errutil.NewAggregate(merr)
}

// handleDelete runs cleanup for resources associated to a Bundle, finally removing the finalizer to unblock the deletion of the object from kubernetes.
func (r *BundleReconciler) handleDelete(ctx context.Context, logger logr.Logger, req ctrl.Request, bundle *fleet.Bundle) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(bundle, finalize.BundleFinalizer) {
		return ctrl.Result{}, nil
	}

	bds, err := r.listBundleDeploymentsForBundle(ctx, bundle)
	if err != nil {
		return ctrl.Result{}, err
	}

	// BundleDeployment deletion happens asynchronously: mark them for deletion and requeue
	// This ensures the Bundle is kept around until all its BundleDeployments are completely deleted.
	// Both GitRepo and HelmOp status reconcilers rely on this condition, as they watch Bundles and not BundleDeployments
	if len(bds) > 0 {
		logger.V(1).Info("Bundle deleted, purging bundle deployments")
		return ctrl.Result{RequeueAfter: requeueAfterBundleDeploymentCleanup}, batchDeleteBundleDeployments(ctx, r.Client, bds)
	}

	if err := r.maybeDeleteOCIArtifact(ctx, bundle); err != nil {
		return ctrl.Result{}, err
	}

	metrics.BundleCollector.Delete(req.Name, req.Namespace)
	controllerutil.RemoveFinalizer(bundle, finalize.BundleFinalizer)
	if err := r.Update(ctx, bundle); err != nil {
		return ctrl.Result{}, err
	}

	// pro-actively delete the bundle's secret. k8s owner garbage collection will handle remaining orphans.
	if err := r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: bundle.Name, Namespace: bundle.Namespace}}); err != nil && !apierrors.IsNotFound(err) {
		logger.V(1).Info("Cannot delete bundle's values secret, owner garbage collection will remove it")
	}

	return ctrl.Result{}, err
}

// ensureFinalizer adds a finalizer to a recently created bundle.
func (r *BundleReconciler) ensureFinalizer(ctx context.Context, bundle *fleet.Bundle) error {
	if controllerutil.ContainsFinalizer(bundle, finalize.BundleFinalizer) {
		return nil
	}

	controllerutil.AddFinalizer(bundle, finalize.BundleFinalizer)
	return r.Update(ctx, bundle)
}

// removeDisplayNameLabel removes the obsolete created-by-display-name label from the bundle if it exists.
func (r *BundleReconciler) removeDisplayNameLabel(ctx context.Context, bundle *fleet.Bundle) error {
	if bundle.Labels == nil {
		return nil
	}

	const deprecatedLabel = "fleet.cattle.io/created-by-display-name"
	if _, exists := bundle.Labels[deprecatedLabel]; !exists {
		return nil
	}

	delete(bundle.Labels, deprecatedLabel)
	if err := r.Update(ctx, bundle); err != nil {
		return fmt.Errorf("%w: %w", fleetutil.ErrRetryable, err)
	}
	return nil
}

func (r *BundleReconciler) createBundleDeployment(
	ctx context.Context,
	l logr.Logger,
	bd *fleet.BundleDeployment,
	contentsInOCI bool,
	contentsInHelmChart bool,
	manifestID string,
) (*fleet.BundleDeployment, error) {
	logger := l.WithValues("deploymentID", bd.Spec.DeploymentID)

	// When content resources are stored in etcd, we need to add finalizers.
	if !contentsInOCI && !contentsInHelmChart {
		content := &fleet.Content{}
		if err := r.Get(ctx, types.NamespacedName{Name: manifestID}, content); err != nil {
			return nil, fmt.Errorf("failed to get content resource: %w", err)
		}

		if added := controllerutil.AddFinalizer(content, bd.Name); added {
			if err := r.Update(ctx, content); err != nil {
				return nil, fmt.Errorf("could not add finalizer to content resource, thus cannot create/update bundledeployment: %w", err)
			}
		}
	}

	updated := bd.DeepCopy()
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, bd, func() error {
		// When this mutation function is called by CreateOrUpdate, bd contains the
		// _old_ bundle deployment, if any.
		// The corresponding Content resource must only be deleted if it is no longer in use, ie if the
		// latest version of the bundle points to a different deployment ID.
		// An empty value for bd.Spec.DeploymentID means that we are deploying the first version of this
		// bundle, hence there are no Contents left over to purge.
		if (!bd.Spec.OCIContents || !contentsInHelmChart) &&
			bd.Spec.DeploymentID != "" &&
			bd.Spec.DeploymentID != updated.Spec.DeploymentID {
			if err := finalize.PurgeContent(ctx, r.Client, bd.Name, bd.Spec.DeploymentID); err != nil {
				logger.Error(err, "Reconcile failed to purge old content resource")
			}
		}

		// check if there's any OCI secret that can be purged
		if err := maybePurgeOCIReferenceSecret(ctx, r.Client, bd, updated); err != nil {
			logger.Error(err, "Reconcile failed to purge old OCI reference secret")
		}

		bd.Spec = updated.Spec
		bd.Labels = updated.GetLabels()

		return nil
	})
	if err != nil {
		logger.Error(err, "Reconcile failed to create or update bundledeployment", "operation", op)
		return nil, err
	}
	logger.Info(upper(op)+" bundledeployment", "operation", op)

	return bd, nil
}

func (r *BundleReconciler) createOptionsSecret(ctx context.Context, bd *fleet.BundleDeployment, options []byte, stagedOptions []byte) error {
	secret := &corev1.Secret{
		Type: fleet.SecretTypeBundleDeploymentOptions,
		ObjectMeta: metav1.ObjectMeta{
			Name:      bd.Name,
			Namespace: bd.Namespace,
		},
	}
	owners := []metav1.OwnerReference{
		{
			APIVersion:         fleet.SchemeGroupVersion.String(),
			Kind:               "BundleDeployment",
			Name:               bd.GetName(),
			UID:                bd.GetUID(),
			BlockOwnerDeletion: ptr.To(true),
			Controller:         ptr.To(true),
		},
	}

	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		secret.OwnerReferences = owners
		secret.Data = map[string][]byte{
			helmvalues.ValuesKey:       options,
			helmvalues.StagedValuesKey: stagedOptions,
		}
		return nil
	}); err != nil {
		return fmt.Errorf("%w: %w", fleetutil.ErrRetryable, err)
	}

	return nil
}

func (r *BundleReconciler) getOCIReference(ctx context.Context, bundle *fleet.Bundle) (string, error) {
	if bundle.Spec.ContentsID == "" {
		return "", fmt.Errorf("cannot get OCI reference. Bundle's ContentsID is not set")
	}
	namespacedName := types.NamespacedName{
		Namespace: bundle.Namespace,
		Name:      bundle.Spec.ContentsID,
	}
	var ociSecret corev1.Secret
	if err := r.Get(ctx, namespacedName, &ociSecret); err != nil {
		return "", fmt.Errorf("%w: %w", fleetutil.ErrRetryable, err)
	}
	ref, ok := ociSecret.Data[ocistorage.OCISecretReference]
	if !ok {
		return "", fmt.Errorf("expected data [reference] not found in secret: %s", bundle.Spec.ContentsID)
	}
	// this is not a valid reference, it is only for display
	return fmt.Sprintf("oci://%s/%s:latest", string(ref), bundle.Spec.ContentsID), nil
}

// cloneConfigMap clones a config map, identified by the provided name and
// namespace, to the namespace of the provided bundle deployment bd. This makes
// the config map available to agents when deploying bd to downstream clusters.
func (r *BundleReconciler) cloneConfigMap(
	ctx context.Context,
	namespace string,
	name string,
	bd *fleet.BundleDeployment,
) error {
	namespacedName := types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}
	var cm corev1.ConfigMap
	if err := r.Get(ctx, namespacedName, &cm); err != nil {
		return fmt.Errorf("failed to load source config map, cannot clone into %q: %w", namespace, err)
	}
	// clone the config map, and just change the namespace so it's in the target's namespace
	targetCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cm.Name,
			Namespace: bd.Namespace,
		},
		Data:       cm.Data,
		BinaryData: cm.BinaryData,
	}

	if err := controllerutil.SetControllerReference(bd, targetCM, r.Scheme); err != nil {
		return err
	}

	updated := targetCM.DeepCopy()
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, targetCM, func() error {
		targetCM.Data = updated.Data
		targetCM.BinaryData = updated.BinaryData

		return nil
	}); err != nil {
		return fmt.Errorf("failed to create or update source config map %s/%s: %w", bd.Namespace, cm.Name, err)
	}

	return nil
}

// cloneSecret clones a secret, identified by the provided secretName and
// namespace, to the namespace of the provided bundle deployment bd. This makes
// the secret available to agents when deploying bd to downstream clusters.
func (r *BundleReconciler) cloneSecret(
	ctx context.Context,
	ns string,
	secretName string,
	secretType string,
	bd *fleet.BundleDeployment,
) error {
	namespacedName := types.NamespacedName{
		Namespace: ns,
		Name:      secretName,
	}
	var secret corev1.Secret
	if err := r.Get(ctx, namespacedName, &secret); err != nil {
		return fmt.Errorf("failed to load source secret, cannot clone into %q: %w", ns, err)
	}
	// clone the secret, and just change the namespace so it's in the target's namespace
	targetSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secret.Name,
			Namespace: bd.Namespace,
		},
		Data:       secret.Data,
		StringData: secret.StringData,
	}

	if secretType != "" {
		targetSecret.Labels = map[string]string{fleet.InternalSecretLabel: "true"}
		targetSecret.Type = corev1.SecretType(secretType)
	}

	if err := controllerutil.SetControllerReference(bd, targetSecret, r.Scheme); err != nil {
		return err
	}

	updated := targetSecret.DeepCopy()
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, targetSecret, func() error {
		targetSecret.Data = updated.Data
		targetSecret.StringData = updated.StringData

		return nil
	}); err != nil {
		return fmt.Errorf("failed to create or update source secret %s/%s: %w", bd.Namespace, secret.Name, err)
	}

	return nil
}

func (r *BundleReconciler) handleContentAccessSecrets(ctx context.Context, bundle *fleet.Bundle, bd *fleet.BundleDeployment) error {
	contentsInOCI := bundle.Spec.ContentsID != "" && ocistorage.OCIIsEnabled()
	contentsInHelmChart := bundle.Spec.HelmOpOptions != nil

	if contentsInOCI {
		if err := r.cloneSecret(
			ctx,
			bundle.Namespace,
			bundle.Spec.ContentsID,
			fleet.SecretTypeOCIStorage,
			bd,
		); err != nil {
			return fmt.Errorf(
				"%w: failed to clone secret %s/%s to downstream cluster namespace: %w",
				fleetutil.ErrRetryable,
				bundle.Namespace,
				bundle.Spec.ContentsID,
				err,
			)
		}
	}
	if contentsInHelmChart && bundle.Spec.HelmOpOptions.SecretName != "" {
		if err := r.cloneSecret(
			ctx,
			bundle.Namespace,
			bundle.Spec.HelmOpOptions.SecretName,
			fleet.SecretTypeHelmOpsAccess,
			bd,
		); err != nil {
			return fmt.Errorf(
				"%w: failed to clone secret %s/%s to downstream cluster namespace: %w",
				fleetutil.ErrRetryable,
				bundle.Namespace,
				bundle.Spec.HelmOpOptions.SecretName,
				err,
			)
		}
	}
	return nil
}

// updateErrorStatus sets the Ready condition in the bundle status and tries to update the resource.
// Setting that condition makes the error message visible in the Rancher UI.
// Upon successful update of the status, updateErrorStatus returns a TerminalError, preventing requeues.
func (r *BundleReconciler) updateErrorStatus(
	ctx context.Context,
	orig, bundle *fleet.Bundle,
	orgErr error,
) error {
	SetCondition(string(fleet.Ready), &bundle.Status, orgErr)

	if statusErr := r.updateStatus(ctx, orig, bundle); statusErr != nil {
		merr := []error{orgErr, fmt.Errorf("failed to update the status: %w", statusErr)}
		return errutil.NewAggregate(merr)
	}

	return reconcile.TerminalError(orgErr)
}

func (r *BundleReconciler) handleDownstreamObjects(ctx context.Context, bundle *fleet.Bundle, bd *fleet.BundleDeployment) error {
	if !experimental.CopyResourcesDownstreamEnabled() {
		return nil
	}

	for _, dr := range bundle.Spec.DownstreamResources {
		switch strings.ToLower(dr.Kind) {
		case "secret":
			if err := r.cloneSecret(ctx, bundle.Namespace, dr.Name, "", bd); err != nil {
				return fmt.Errorf(
					"%w: failed to copy secret %s/%s to downstream cluster namespace: %w",
					fleetutil.ErrRetryable,
					bundle.Namespace,
					dr.Name,
					err,
				)
			}
		case "configmap":
			if err := r.cloneConfigMap(ctx, bundle.Namespace, dr.Name, bd); err != nil {
				return fmt.Errorf(
					"%w: failed to copy config map %s/%s to downstream cluster namespace: %w",
					fleetutil.ErrRetryable,
					bundle.Namespace,
					dr.Name,
					err,
				)
			}
		default:
			return fmt.Errorf("unsupported kind for object to copy to downstream: %q", dr.Kind)
		}
	}

	return nil
}

// updateStatus patches the status of the bundle and collects metrics upon a successful update of
// the bundle status. It returns nil if the status update is successful, otherwise it returns an
// error.
func (r *BundleReconciler) updateStatus(ctx context.Context, orig *fleet.Bundle, bundle *fleet.Bundle) error {
	logger := log.FromContext(ctx).WithName("bundle - updateStatus")
	statusPatch := client.MergeFrom(orig)

	if patchData, err := statusPatch.Data(bundle); err == nil && string(patchData) == "{}" {
		// skip update if patch is empty
		return nil
	}
	if err := r.Status().Patch(ctx, bundle, statusPatch); err != nil {
		logger.V(1).Info("Reconcile failed update to bundle status", "status", bundle.Status, "error", err)
		return err
	}
	metrics.BundleCollector.Collect(ctx, bundle)
	return nil
}

func (r *BundleReconciler) listBundleDeploymentsForBundle(ctx context.Context, bundle *fleet.Bundle) ([]fleet.BundleDeployment, error) {
	list := &fleet.BundleDeploymentList{}
	if err := r.List(ctx, list,
		client.MatchingLabels{
			fleet.BundleLabel:          bundle.GetName(),
			fleet.BundleNamespaceLabel: bundle.GetNamespace(),
		},
	); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// cleanupOrphanedBundleDeployments will delete all existing BundleDeployments which do not have a match in a provided list of UIDs
func (r *BundleReconciler) cleanupOrphanedBundleDeployments(ctx context.Context, bundle *fleet.Bundle, uidsToKeep sets.Set[types.UID]) error {
	list, err := r.listBundleDeploymentsForBundle(ctx, bundle)
	if err != nil {
		return err
	}
	toDelete := slices.DeleteFunc(list, func(bd fleet.BundleDeployment) bool {
		// don't delete BundleDeployments that are not in schedule as
		// that would uninstall the deployment in the agent
		return uidsToKeep.Has(bd.UID) || bd.Spec.OffSchedule
	})
	return batchDeleteBundleDeployments(ctx, r.Client, toDelete)
}

func (r *BundleReconciler) maybeDeleteOCIArtifact(ctx context.Context, bundle *fleet.Bundle) error {
	if bundle.Spec.ContentsID == "" {
		return nil
	}

	secretID := client.ObjectKey{Name: bundle.Spec.ContentsID, Namespace: bundle.Namespace}
	opts, err := ocistorage.ReadOptsFromSecret(ctx, r.Client, secretID)
	if err != nil {
		return err
	}
	err = ocistorage.NewOCIWrapper().DeleteManifest(ctx, opts, bundle.Spec.ContentsID)
	if err != nil {
		r.Recorder.Event(bundle, fleetevent.Warning, "FailedToDeleteOCIArtifact", fmt.Sprintf("deleting OCI artifact %q: %v", bundle.Spec.ContentsID, err.Error()))
	}

	// In case there's an error deleting from the OCI registry,
	// we return nil because otherwise the controller would retry the operation,
	// and since the registry is not a component of Fleet,
	// we don't have full control over it.
	return nil
}

// computeResult computes the controller result and error to return, depending on whether err is a retryable
// error, which will be wrapped with the provided prefix.
// If err is non-retryable, it will be propagated to the bundle status.
func (r *BundleReconciler) computeResult(
	ctx context.Context,
	logger logr.Logger,
	bundleOrig,
	bundle *fleet.Bundle,
	prefix string,
	err error,
) (ctrl.Result, error) {
	if done, res, ctrlErr := CheckRetryable(err, logger); done {
		return res, ctrlErr
	}

	err = fmt.Errorf("%s: %w", prefix, err)

	SetCondition(string(fleet.Ready), &bundle.Status, err)

	return ctrl.Result{}, r.updateErrorStatus(ctx, bundleOrig, bundle, err)
}

func batchDeleteBundleDeployments(ctx context.Context, c client.Client, list []fleet.BundleDeployment) error {
	var errs []error
	for _, bd := range list {
		if bd.DeletionTimestamp != nil {
			// already being deleted
			continue
		}
		// Mark the object for deletion. The BundleDeployment reconciler will react to that calling PurgeContent and finally removing the finalizer
		if err := c.Delete(ctx, &bd); client.IgnoreNotFound(err) != nil {
			errs = append(errs, err)
		}

		// k8s ownership garbage collection can take a long time, so we explicitly delete the secrets here.
		// GC will delete any remaining orphaned secrets, no need to add an error.
		_ = c.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: bd.Name, Namespace: bd.Namespace}})
	}

	return errors.Join(errs...)
}

// clusterChangedPredicate filters cluster events that relate to bundldeployment creation.
func clusterChangedPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			n := e.ObjectNew.(*fleet.Cluster)
			o := e.ObjectOld.(*fleet.Cluster)
			// cluster deletion will eventually trigger a delete event
			if n == nil || !n.DeletionTimestamp.IsZero() {
				return true
			}
			// labels and annotations are used for templating and targeting
			if !maps.Equal(n.Labels, o.Labels) {
				return true
			}
			if !maps.Equal(n.Annotations, o.Annotations) {
				return true
			}
			// spec templateValues is used in templating
			if !reflect.DeepEqual(n.Spec, o.Spec) {
				return true
			}
			// this namespace contains the bundledeployments
			if n.Status.Namespace != o.Status.Namespace {
				return true
			}
			// this namespace indicates the agent is running
			if n.Status.Agent.Namespace != o.Status.Agent.Namespace {
				return true
			}

			if n.Status.Scheduled != o.Status.Scheduled {
				return true
			}

			if n.Status.ActiveSchedule != o.Status.ActiveSchedule {
				return true
			}

			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return true
		},
	}
}

// loadBundleValues loads the values from the secret and sets them in the bundle spec
func loadBundleValues(ctx context.Context, c client.Client, bundle *fleet.Bundle) error {
	secret := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Name: bundle.Name, Namespace: bundle.Namespace}, secret); apierrors.IsNotFound(err) {
		return fmt.Errorf("%w, retrying to get values secret for bundle: %w", fleetutil.ErrRetryable, err)
	} else if err != nil {
		return fmt.Errorf("failed to get values secret for bundle: %w", err)
	}

	hash, err := helmvalues.HashValuesSecret(secret.Data)
	if err != nil {
		return fmt.Errorf("failed to hash values secret %q: %w", secret.Name, err)
	}

	if bundle.Spec.ValuesHash != hash {
		return fmt.Errorf("%w, retrying since bundle values secret has changed, expected hash %q, calculated %q", fleetutil.ErrRetryable, bundle.Spec.ValuesHash, hash)
	}

	if err := helmvalues.SetValues(bundle, secret.Data); err != nil {
		return fmt.Errorf("failed to set values from secret %q: %w", secret.Name, err)
	}

	return nil
}

// maybePurgeOCIReferenceSecret deletes an outdated OCI reference secret if necessary, i.e. if a bundle has been updated
// and either of the following applies:
// * the old bundle used OCI storage while the new one does not anymore
// * both old and new bundles use OCI storage, but the deployment ID has changed between the old bundles and the new one.
func maybePurgeOCIReferenceSecret(ctx context.Context, c client.Client, old, new *fleet.BundleDeployment) error {
	if !old.Spec.OCIContents || old.Spec.DeploymentID == "" {
		return nil
	}

	if !new.Spec.OCIContents || (old.Spec.DeploymentID != new.Spec.DeploymentID) {
		id, _ := kv.Split(old.Spec.DeploymentID, ":")
		var secret corev1.Secret
		secretID := client.ObjectKey{Name: id, Namespace: old.Namespace}
		if err := c.Get(ctx, secretID, &secret); err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}
		} else {
			if err := c.Delete(ctx, &secret); err != nil {
				return err
			}
		}
	}

	return nil
}

func upper(op controllerutil.OperationResult) string {
	switch op {
	case controllerutil.OperationResultNone:
		return "Unchanged"
	case controllerutil.OperationResultCreated:
		return "Created"
	case controllerutil.OperationResultUpdated:
		return "Updated"
	case controllerutil.OperationResultUpdatedStatus:
		return "Updated"
	case controllerutil.OperationResultUpdatedStatusOnly:
		return "Updated"
	default:
		return "Unknown"
	}
}

// BundleDeploymentMapFunc returns a function that maps a BundleDeployment to a Bundle.
func BundleDeploymentMapFunc(r *BundleReconciler) func(ctx context.Context, a client.Object) []reconcile.Request {
	return func(ctx context.Context, a client.Object) []reconcile.Request {
		bd, ok := a.(*fleet.BundleDeployment)
		if !ok {
			return nil
		}

		if !sharding.ShouldProcess(bd, r.ShardID) {
			return nil
		}
		labels := bd.GetLabels()
		if labels == nil {
			return nil
		}

		ns, name := target.BundleFromDeployment(labels)
		if ns != "" && name != "" {
			return []reconcile.Request{{
				NamespacedName: types.NamespacedName{
					Namespace: ns,
					Name:      name,
				},
			}}
		}

		return nil
	}
}
