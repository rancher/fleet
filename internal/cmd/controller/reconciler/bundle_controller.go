// Copyright (c) 2021-2023 SUSE LLC

package reconciler

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/go-logr/logr"

	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/internal/metrics"
	"github.com/rancher/fleet/internal/ociwrapper"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const bundleFinalizer = "fleet.cattle.io/bundle-finalizer"

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
	Scheme *runtime.Scheme

	Builder TargetBuilder
	Store   Store
	Query   BundleQuery
	ShardID string

	Workers int
}

// SetupWithManager sets up the controller with the Manager.
func (r *BundleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.Bundle{}).
		// Note: Maybe improve with WatchesMetadata, does it have access to labels?
		Watches(
			// Fan out from bundledeployment to bundle
			&fleet.BundleDeployment{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, a client.Object) []ctrl.Request {
				bd := a.(*fleet.BundleDeployment)
				labels := bd.GetLabels()
				if labels == nil {
					return nil
				}

				ns, name := target.BundleFromDeployment(labels)
				if ns != "" && name != "" {
					return []ctrl.Request{{
						NamespacedName: types.NamespacedName{
							Namespace: ns,
							Name:      name,
						},
					}}
				}

				return nil
			}),
			builder.WithPredicates(bundleDeploymentStatusChangedPredicate()),
		).
		Watches(
			// Fan out from cluster to bundle
			&fleet.Cluster{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, a client.Object) []ctrl.Request {
				cluster := a.(*fleet.Cluster)
				bundlesToRefresh, _, err := r.Query.BundlesForCluster(ctx, cluster)
				if err != nil {
					return nil
				}
				requests := []ctrl.Request{}
				for _, bundle := range bundlesToRefresh {
					requests = append(requests, ctrl.Request{
						NamespacedName: types.NamespacedName{
							Namespace: bundle.Namespace,
							Name:      bundle.Name,
						},
					})
				}

				return requests
			}),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
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
	bundleOrig := bundle.DeepCopy()

	if bundle.Labels[fleet.RepoLabel] != "" {
		logger = logger.WithValues(
			"gitrepo", bundle.Labels[fleet.RepoLabel],
			"commit", bundle.Labels[fleet.CommitLabel],
		)
	}

	if res, err := r.addOrRemoveFinalizer(ctx, logger, req, bundle); res {
		return ctrl.Result{}, err
	}

	logger.V(1).Info(
		"Reconciling bundle, checking targets, calculating changes, building objects",
		"generation",
		bundle.Generation,
		"observedGeneration",
		bundle.Status.ObservedGeneration,
	)

	// if the bundle has the helmops options set but the experimental flag is not
	// set we don't deploy the bundle.
	// This is to avoid intentional or accidental deployment of bundles with no
	// resources or not well defined.
	if bundle.Spec.HelmAppOptions != nil && !experimentalHelmOpsEnabled() {
		return ctrl.Result{}, fmt.Errorf("bundle contains data used by helm ops but env variable EXPERIMENTAL_HELM_OPS is not set to true")
	}
	contentsInOCI := bundle.Spec.ContentsID != "" && ociwrapper.ExperimentalOCIIsEnabled()
	contentsInHelmChart := bundle.Spec.HelmAppOptions != nil
	manifestID := bundle.Spec.ContentsID
	var resourcesManifest *manifest.Manifest
	if !contentsInOCI && !contentsInHelmChart {
		resourcesManifest = manifest.FromBundle(bundle)
		if bundle.Generation != bundle.Status.ObservedGeneration {
			resourcesManifest.ResetSHASum()
		}

		manifestDigest, err := resourcesManifest.SHASum()
		if err != nil {
			return ctrl.Result{}, err
		}
		bundle.Status.ResourcesSHA256Sum = manifestDigest

		manifestID, err = resourcesManifest.ID()
		if err != nil {
			// this should never happen, since manifest.SHASum() cached the result and worked above.
			return ctrl.Result{}, err
		}
	}

	matchedTargets, err := r.Builder.Targets(ctx, bundle, manifestID)
	if err != nil {
		// When targeting fails, we don't want to continue and make the error message visible in the
		// UI. For that we use a status condition of type Ready.
		bundle.Status.Conditions = []genericcondition.GenericCondition{
			{
				Type:           string(fleet.Ready),
				Status:         v1.ConditionFalse,
				Message:        "Targeting error: " + err.Error(),
				LastUpdateTime: metav1.Now().UTC().Format(time.RFC3339),
			},
		}

		err := r.updateStatus(ctx, bundleOrig, bundle)
		return ctrl.Result{}, err
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
			return ctrl.Result{}, err
		}
	}

	if err := resetStatus(&bundle.Status, matchedTargets); err != nil {
		return ctrl.Result{}, err
	}

	// this will add the defaults for a new bundledeployment
	if err := target.UpdatePartitions(&bundle.Status, matchedTargets); err != nil {
		return ctrl.Result{}, err
	}

	if contentsInOCI {
		url, err := r.getOCIReference(ctx, bundle)
		if err != nil {
			return ctrl.Result{}, err
		}
		bundle.Status.OCIReference = url
	}

	setResourceKey(&bundle.Status, matchedTargets)

	summary.SetReadyConditions(&bundle.Status, "Cluster", bundle.Status.Summary)
	bundle.Status.ObservedGeneration = bundle.Generation

	// build BundleDeployments out of targets discarding Status, replacing DependsOn with the
	// bundle's DependsOn (pure function) and replacing the labels with the bundle's labels
	for _, target := range matchedTargets {
		bd, err := r.createBundleDeployment(
			ctx,
			logger,
			target,
			contentsInOCI,
			bundle.Spec.HelmAppOptions,
			manifestID)
		if err != nil {
			return ctrl.Result{}, err
		}

		if err := r.handleDeploymentSecret(ctx, bundle, bd); err != nil {
			return ctrl.Result{}, err
		}
	}

	updateDisplay(&bundle.Status)
	err = r.updateStatus(ctx, bundleOrig, bundle)

	return ctrl.Result{}, err
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

// addOrRemoveFinalizer adds a finalizer to a recently created bundle, or removes it on a bundle marked for deletion.
// It returns a boolean indicating whether the current reconcile loop should stop, along with any error which may have
// occurred in the process.
func (r *BundleReconciler) addOrRemoveFinalizer(ctx context.Context, logger logr.Logger, req ctrl.Request, bundle *fleet.Bundle) (bool, error) {
	if !bundle.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(bundle, bundleFinalizer) {
			metrics.BundleCollector.Delete(req.Name, req.Namespace)

			logger.V(1).Info("Bundle not found, purging bundle deployments")
			if err := finalize.PurgeBundleDeployments(ctx, r.Client, req.NamespacedName); err != nil {
				// A bundle deployment may have been purged by the GitRepo reconciler, hence we ignore
				// not-found errors here.
				return true, client.IgnoreNotFound(err)
			}

			controllerutil.RemoveFinalizer(bundle, bundleFinalizer)
			err := r.Update(ctx, bundle)
			if client.IgnoreNotFound(err) != nil {
				return true, err
			}
		}

		return true, nil
	}

	if !controllerutil.ContainsFinalizer(bundle, bundleFinalizer) {
		controllerutil.AddFinalizer(bundle, bundleFinalizer)
		err := r.Update(ctx, bundle)
		if client.IgnoreNotFound(err) != nil {
			return true, err
		}
	}

	return false, nil
}

func (r *BundleReconciler) createBundleDeployment(
	ctx context.Context,
	logger logr.Logger,
	target *target.Target,
	contentsInOCI bool,
	helmAppOptions *fleet.BundleHelmOptions,
	manifestID string,
) (*fleet.BundleDeployment, error) {
	if target.Deployment == nil {
		return nil, nil
	}
	if target.Deployment.Namespace == "" {
		logger.V(1).Info(
			"Skipping bundledeployment with empty namespace, "+
				"waiting for agentmanagement to set cluster.status.namespace",
			"bundledeployment",
			target.Deployment,
		)
		return nil, nil
	}

	// NOTE we don't use the existing BundleDeployment, we discard annotations, status, etc
	// copy labels from Bundle as they might have changed
	bd := target.BundleDeployment()

	// No need to check the deletion timestamp here before adding a finalizer, since the bundle has just
	// been created.
	controllerutil.AddFinalizer(bd, bundleDeploymentFinalizer)

	bd.Spec.OCIContents = contentsInOCI
	bd.Spec.HelmChartOptions = helmAppOptions

	// contents resources stored in etcd, finalizers to add here.
	if !contentsInOCI {
		content := &fleet.Content{}
		err := r.Get(ctx, types.NamespacedName{Name: manifestID}, content)
		if client.IgnoreNotFound(err) != nil {
			logger.Error(err, "Reconcile failed to get content", "content ID", manifestID)
			return nil, err
		}

		if added := controllerutil.AddFinalizer(content, bd.Name); added {
			if err := r.Update(ctx, content); err != nil {
				logger.Error(err, "Reconcile failed to add content finalizer", "content ID", manifestID)
				return nil, err
			}
		}
	}

	contentsInHelmChart := helmAppOptions != nil
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
				logger.Error(
					err,
					"Reconcile failed to purge old content resource",
					"bundledeployment",
					bd,
					"deploymentID",
					bd.Spec.DeploymentID,
				)
			}
		}

		bd.Spec = updated.Spec
		bd.Labels = updated.GetLabels()

		return nil
	})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		logger.Error(
			err,
			"Reconcile failed to create or update bundledeployment",
			"bundledeployment",
			bd,
			"operation",
			op,
		)
		return nil, err
	}
	logger.V(1).Info(upper(op)+" bundledeployment", "bundledeployment", bd, "operation", op)

	return bd, nil
}

func (r *BundleReconciler) createDeploymentSecret(ctx context.Context, secretName string, bundle *fleet.Bundle, bd *fleet.BundleDeployment) error {
	namespacedName := types.NamespacedName{
		Namespace: bundle.Namespace,
		Name:      secretName,
	}
	var secret corev1.Secret
	if err := r.Get(ctx, namespacedName, &secret); err != nil {
		return err
	}
	// clone the secret, and just change the namespace so it's in the target's namespace
	targetSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secret.Name,
			Namespace: bd.Namespace,
		},
		Data: secret.Data,
	}

	if err := controllerutil.SetControllerReference(bd, targetSecret, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, targetSecret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
	}

	return nil
}

func (r *BundleReconciler) getOCIReference(ctx context.Context, bundle *fleet.Bundle) (string, error) {
	if bundle.Spec.ContentsID == "" {
		return "", fmt.Errorf("cannot get OCI reference. Bundles's ContentsID is not set")
	}
	namespacedName := types.NamespacedName{
		Namespace: bundle.Namespace,
		Name:      bundle.Spec.ContentsID,
	}
	var ociSecret corev1.Secret
	if err := r.Get(ctx, namespacedName, &ociSecret); err != nil {
		return "", err
	}
	ref, ok := ociSecret.Data[ociwrapper.OCISecretReference]
	if !ok {
		return "", fmt.Errorf("expected data [reference] not found in secret: %s", bundle.Spec.ContentsID)
	}
	// this is not a valid reference, it is only for display
	return fmt.Sprintf("oci://%s/%s:latest", string(ref), bundle.Spec.ContentsID), nil
}

func (r *BundleReconciler) handleDeploymentSecret(ctx context.Context, bundle *fleet.Bundle, bd *fleet.BundleDeployment) error {
	if bd == nil {
		return nil
	}
	contentsInOCI := bundle.Spec.ContentsID != "" && ociwrapper.ExperimentalOCIIsEnabled()
	contentsInHelmChart := bundle.Spec.HelmAppOptions != nil && experimentalHelmOpsEnabled()

	if contentsInOCI {
		return r.createDeploymentSecret(ctx, bundle.Spec.ContentsID, bundle, bd)
	}
	if contentsInHelmChart && bundle.Spec.HelmAppOptions.SecretName != "" {
		return r.createDeploymentSecret(ctx, bundle.Spec.HelmAppOptions.SecretName, bundle, bd)
	}
	return nil
}

// experimentalHelmOpsEnabled returns true if the EXPERIMENTAL_HELM_OPS env variable is set to true
// returns false otherwise
func experimentalHelmOpsEnabled() bool {
	value, err := strconv.ParseBool(os.Getenv("EXPERIMENTAL_HELM_OPS"))
	return err == nil && value
}

// updateStatus patches the status of the bundle and collects metrics for the update of the bundle
// status. It returns nil if the status update is successful, otherwise it returns an error.
func (r *BundleReconciler) updateStatus(ctx context.Context, orig *fleet.Bundle, bundle *fleet.Bundle) error {
	logger := log.FromContext(ctx).WithName("bundle - updateStatus")
	statusPatch := client.MergeFrom(orig)
	err := r.Status().Patch(ctx, bundle, statusPatch)
	if err != nil {
		logger.V(1).Info("Reconcile failed update to bundle status", "status", bundle.Status, "error", err)
		return err
	}
	metrics.BundleCollector.Collect(ctx, bundle)
	return nil
}
