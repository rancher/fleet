package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rancher/fleet/internal/cmd/agent/deployer"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/cleanup"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/driftdetect"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/monitor"
	"github.com/rancher/fleet/internal/helmvalues"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
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
		err := r.Cleanup.CleanupReleases(ctx, key, nil)
		if err != nil {
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

	var merr []error

	// helm deploy the bundledeployment
	if status, err := r.Deployer.DeployBundle(ctx, bd); err != nil {
		logger.V(1).Info("Failed to deploy bundle", "status", status, "error", err)

		// do not use the returned status, instead set the condition and possibly a timestamp
		bd.Status = setCondition(bd.Status, err, monitor.Cond(fleetv1.BundleDeploymentConditionDeployed))

		// Not-ready dependencies should not be treated as an error.
		// Instead, a controlled requeue should happen until the conditions
		var notReadyDependenciesError *deployer.NotReadyDependenciesError
		if errors.As(err, &notReadyDependenciesError) {
			if err := r.updateStatus(ctx, orig, bd); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: durations.WaitForDependenciesReadyRequeueInterval}, nil
		}

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
