package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rancher/fleet/internal/cmd/agent/deployer"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/cleanup"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/driftdetect"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/monitor"
	"github.com/rancher/fleet/internal/experimental"
	"github.com/rancher/fleet/internal/helmvalues"
	"github.com/rancher/fleet/internal/namespaces"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// BundleDeploymentReconciler reconciles a BundleDeployment object, by
// deploying the bundle as a helm release.
type BundleDeploymentReconciler struct {
	client.Client
	Reader client.Reader

	Scheme *runtime.Scheme

	// LocalClient is the client for the cluster the agent is running on.
	LocalClient client.Client

	Deployer    *deployer.Deployer
	Monitor     *monitor.Monitor
	DriftDetect *driftdetect.DriftDetect
	Cleanup     *cleanup.Cleanup

	DefaultNamespace string

	// AgentInfo is the labelSuffix used by the helm deployer
	AgentScope string

	Workers int
}

var DefaultRetry = wait.Backoff{
	Steps:    5,
	Duration: 5 * time.Second,
	Factor:   1.0,
	Jitter:   0.1,
}

// SetupWithManager sets up the controller with the Manager.
func (r *BundleDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleetv1.BundleDeployment{}).
		WithEventFilter(
			// we do not trigger for status changes
			predicate.Or(
				// Note: These predicates prevent cache
				// syncPeriod from triggering reconcile, since
				// cache sync is an Update event.
				predicate.GenerationChangedPredicate{},
				predicate.AnnotationChangedPredicate{},
				predicate.LabelChangedPredicate{},
				predicate.Funcs{
					// except for changes to status.Refresh
					UpdateFunc: func(e event.UpdateEvent) bool {
						n := e.ObjectNew.(*fleetv1.BundleDeployment)
						o := e.ObjectOld.(*fleetv1.BundleDeployment)
						if n == nil || o == nil {
							return false
						}
						return n.Status.SyncGeneration != o.Status.SyncGeneration
					},
					DeleteFunc: func(e event.DeleteEvent) bool {
						return true
					},
				},
			)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)
}

//+kubebuilder:rbac:groups=fleet.cattle.io,resources=bundledeployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=bundledeployments/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=bundledeployments/finalizers,verbs=update

// Reconcile compares the state specified by the BundleDeployment object
// against the actual state, and decides if the bundle should be deployed.
// The deployed resources are then monitored for drift.
// It also updates the status of the BundleDeployment object with the results.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *BundleDeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("bundledeployment")
	ctx = log.IntoContext(ctx, logger)
	key := req.String()

	// get latest BundleDeployment from cluster
	bd := &fleetv1.BundleDeployment{}
	err := r.Get(ctx, req.NamespacedName, bd)
	if apierrors.IsNotFound(err) {
		// This actually deletes the helm releases if a bundledeployment is deleted or orphaned
		logger.V(1).Info("BundleDeployment deleted, cleaning up helm releases")
		if err := r.Cleanup.CleanupReleases(ctx, key, nil); err != nil {
			logger.Error(err, "Failed to clean up missing bundledeployment", "key", key)
		}

		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}
	orig := bd.DeepCopy()

	if bd.Spec.Paused {
		logger.V(1).Info("Bundle paused, clearing drift detection")
		err := r.DriftDetect.Clear(req.String())

		return ctrl.Result{}, err
	}
	if bd.Spec.OffSchedule {
		logger.V(1).Info("Bundle not in schedule, clearing drift detection")
		err := r.DriftDetect.Clear(req.String())

		return ctrl.Result{}, err
	}

	// load the bundledeployment options from the secret, if present
	if bd.Spec.ValuesHash != "" {
		secret := &corev1.Secret{}
		if err := r.Reader.Get(ctx, client.ObjectKey{Namespace: bd.Namespace, Name: bd.Name}, secret); err != nil {
			return ctrl.Result{}, err
		}

		h := helmvalues.HashOptions(secret.Data[helmvalues.ValuesKey], secret.Data[helmvalues.StagedValuesKey])
		if h != bd.Spec.ValuesHash {
			return ctrl.Result{}, fmt.Errorf("retrying, hash mismatch between secret and bundledeployment: actual %s != expected %s", h, bd.Spec.ValuesHash)
		}

		if err := helmvalues.SetOptions(bd, secret.Data); err != nil {
			return ctrl.Result{}, err
		}
	}

	forceDeploy, err := r.copyResourcesFromUpstream(ctx, bd, logger)
	if err != nil {
		return ctrl.Result{}, err
	}

	var merr []error

	// helm deploy the bundledeployment
	if status, err := r.Deployer.DeployBundle(ctx, bd, forceDeploy); err != nil {
		logger.V(1).Info("Failed to deploy bundle", "status", status, "error", err)

		// do not use the returned status, instead set the condition and possibly a timestamp
		bd.Status = setCondition(bd.Status, err, monitor.Cond(fleetv1.BundleDeploymentConditionDeployed))

		merr = append(merr, fmt.Errorf("failed deploying bundle: %w", err))
	} else {
		logger.V(1).Info("Bundle deployed", "status", status)
		bd.Status = setCondition(status, nil, monitor.Cond(fleetv1.BundleDeploymentConditionDeployed))
	}

	// retrieve the resources from the helm history.
	// if we can't retrieve the resources, we don't need to try any of the other operations and requeue now
	resources, err := r.Deployer.Resources(bd.Name, bd.Status.Release)
	if err != nil {
		logger.V(1).Info("Failed to retrieve bundledeployment's resources")
		if statusErr := r.updateStatus(ctx, orig, bd); statusErr != nil {
			merr = append(merr, err)
			merr = append(merr, fmt.Errorf("failed to update the status: %w", statusErr))
		}
		return ctrl.Result{}, errutil.NewAggregate(merr)
	}

	if monitor.ShouldUpdateStatus(bd) {
		// update the bundledeployment status and check if we deploy an agent
		status, err := r.Monitor.UpdateStatus(ctx, bd, resources)
		if err != nil {
			logger.Error(err, "Cannot monitor deployed bundle")

			// if there is an error, do not use the returned status from monitor
			bd.Status = setCondition(bd.Status, err, monitor.Cond(fleetv1.BundleDeploymentConditionMonitored))
			merr = append(merr, fmt.Errorf("failed updating status: %w", err))
		} else {
			// we add to the status from deployer.DeployBundle
			bd.Status = setCondition(status, nil, monitor.Cond(fleetv1.BundleDeploymentConditionMonitored))
		}

		if len(bd.Status.ModifiedStatus) > 0 && monitor.ShouldRedeployAgent(bd) {
			bd.Status.AppliedDeploymentID = ""
			if err := r.Cleanup.OldAgent(ctx, status.ModifiedStatus); err != nil {
				merr = append(merr, fmt.Errorf("failed cleaning old agent: %w", err))
			}
		}
	}

	// update our driftdetect mini controller, which watches deployed resources for drift
	if err := r.DriftDetect.Refresh(ctx, req.String(), bd, resources); err != nil {
		logger.V(1).Info("Failed to refresh drift detection", "step", "drift", "error", err)
		merr = append(merr, fmt.Errorf("failed refreshing drift detection: %w", err))
	}

	if err := r.Cleanup.CleanupReleases(ctx, key, bd); err != nil {
		logger.V(1).Info("Failed to clean up bundledeployment releases", "error", err)
	}

	// final status update
	logger.V(1).Info("Reconcile finished, updating the bundledeployment status")
	if err := r.updateStatus(ctx, orig, bd); apierrors.IsNotFound(err) {
		merr = append(merr, fmt.Errorf("bundledeployment has been deleted: %w", err))
	} else if err != nil {
		merr = append(merr, fmt.Errorf("failed final update to bundledeployment status: %w", err))
	}

	return ctrl.Result{}, errutil.NewAggregate(merr)
}

// copyResourcesFromUpstream copies bd's DownstreamResources, from the downstream cluster's namespace on the management
// cluster to the destination namespace on the downstream cluster, creating that namespace if needed.
// If bd does not have any DownstreamResources, this method does not issue any API server calls.
func (r *BundleDeploymentReconciler) copyResourcesFromUpstream(
	ctx context.Context,
	bd *fleetv1.BundleDeployment,
	logger logr.Logger,
) (bool, error) {
	if !experimental.CopyResourcesDownstreamEnabled() {
		return false, nil
	}

	if len(bd.Spec.Options.DownstreamResources) == 0 {
		return false, nil
	}

	destNS := namespaces.GetDeploymentNS(r.DefaultNamespace, bd.Spec.Options)

	ns := corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: destNS}}
	if err := r.LocalClient.Get(ctx, types.NamespacedName{Name: ns.Name}, &ns); apierrors.IsNotFound(err) {
		if err := r.LocalClient.Create(ctx, &ns); err != nil {
			logger.Info(err.Error())
			return false, err
		}

		logger.V(1).Info("Created namespace to copy resources from upstream", "namespace", ns.Name)
	}

	requiresBDUpdate := false

	for _, rsc := range bd.Spec.Options.DownstreamResources {
		switch strings.ToLower(rsc.Kind) {
		case "secret":
			var s corev1.Secret
			if err := r.Reader.Get(ctx, client.ObjectKey{Namespace: bd.Namespace, Name: rsc.Name}, &s); err != nil {
				// The bundle deployment is actually created by the bundle reconciler _before_
				// these objects are copied to the cluster's namespace, hence retries should happen if
				// they are not found.

				return false, fmt.Errorf(
					"could not get secret %s/%s from upstream namespace for copying: %w",
					bd.Namespace,
					rsc.Name,
					err,
				)
			}

			s.Namespace = destNS
			s.ResourceVersion = ""
			if s.Labels == nil {
				s.Labels = map[string]string{}
			}
			s.Labels[fleetv1.BundleDeploymentOwnershipLabel] = bd.Name

			updated := s.DeepCopy()
			op, err := controllerutil.CreateOrUpdate(ctx, r.LocalClient, &s, func() error {
				s.Data = updated.Data
				s.StringData = updated.StringData

				return nil
			})
			if err != nil {
				return false, fmt.Errorf("failed to create or update secret %s/%s downstream: %v", bd.Namespace, rsc.Name, err)
			}

			requiresBDUpdate = op == controllerutil.OperationResultUpdated

		case "configmap":
			var cm corev1.ConfigMap
			if err := r.Reader.Get(ctx, client.ObjectKey{Namespace: bd.Namespace, Name: rsc.Name}, &cm); err != nil {
				// The bundle deployment is actually created by the bundle reconciler _before_
				// these objects are copied to the cluster's namespace, hence retries should happen if
				// they are not found.

				return false, fmt.Errorf(
					"could not get config map %s/%s from upstream namespace for copying: %w",
					bd.Namespace,
					rsc.Name,
					err,
				)
			}
			cm.Namespace = destNS
			cm.ResourceVersion = ""
			if cm.Labels == nil {
				cm.Labels = map[string]string{}
			}
			cm.Labels[fleetv1.BundleDeploymentOwnershipLabel] = bd.Name

			updated := cm.DeepCopy()
			op, err := controllerutil.CreateOrUpdate(ctx, r.LocalClient, &cm, func() error {
				cm.Data = updated.Data
				cm.BinaryData = updated.BinaryData

				return nil
			})
			if err != nil {
				return false, fmt.Errorf("failed to create or update configmap %s/%s downstream: %v", bd.Namespace, rsc.Name, err)
			}

			requiresBDUpdate = op == controllerutil.OperationResultUpdated
		default:
			return false, fmt.Errorf("unknown resource type for copy to downstream cluster: %q", rsc.Kind)
		}
	}

	return requiresBDUpdate, nil
}

func (r *BundleDeploymentReconciler) updateStatus(ctx context.Context, orig *fleetv1.BundleDeployment, obj *fleetv1.BundleDeployment) error {
	statusPatch := client.MergeFrom(orig)
	if patchData, err := statusPatch.Data(obj); err == nil && string(patchData) == "{}" {
		// skip update if patch is empty
		return nil
	}
	return r.Status().Patch(ctx, obj, statusPatch)
}

// setCondition sets the condition and updates the timestamp, if the condition changed
func setCondition(newStatus fleetv1.BundleDeploymentStatus, err error, cond monitor.Cond) fleetv1.BundleDeploymentStatus {
	cond.SetError(&newStatus, "", ignoreConflict(err))
	return newStatus
}

func ignoreConflict(err error) error {
	if apierrors.IsConflict(err) {
		return nil
	}
	return err
}
