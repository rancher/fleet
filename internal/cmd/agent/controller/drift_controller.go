package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/fleet/internal/cmd/agent/deployer"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/driftdetect"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/monitor"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/condition"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type DriftReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Deployer    *deployer.Deployer
	Monitor     *monitor.Monitor
	DriftDetect *driftdetect.DriftDetect

	DriftChan chan event.TypedGenericEvent[*fleetv1.BundleDeployment]

	Workers int
}

// enqueueDelay is used as an artificial delay for enqueuing BundleDeployment reconciliation requests
// This allows aggregating multiple consecutive events on deployed resources, reducing the number of BundleDeployment (and Bundle) reconciliations at the cost of introducing a delay in the notification
// TODO: make this configurable?
const enqueueDelay = 5 * time.Second

// SetupWithManager sets up the controller with the Manager.
func (r *DriftReconciler) SetupWithManager(mgr ctrl.Manager) error {
	src := source.Channel(r.DriftChan, enqueueRequestHandlerWithDelay(enqueueDelay))
	return ctrl.NewControllerManagedBy(mgr).
		Named("drift-reconciler").
		WatchesRawSource(src).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)

}

// Reconcile is triggered via a channel from the driftdetect mini controller,
// which watches deployed resources for drift. It does so by creating a plan
// and comparing it to the current state.
// It will update the status of the BundleDeployment and correct drift if enabled.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *DriftReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("drift")
	ctx = log.IntoContext(ctx, logger)

	// get latest BundleDeployment from cluster
	bd := &fleetv1.BundleDeployment{}
	err := r.Get(ctx, req.NamespacedName, bd)
	if apierrors.IsNotFound(err) {
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	orig := bd.DeepCopy()
	if bd.Spec.CorrectDrift != nil {
		logger = logger.WithValues("enabled", bd.Spec.CorrectDrift.Enabled, "force", bd.Spec.CorrectDrift.Force)
	}

	if bd.Spec.Paused {
		logger.V(1).Info("Bundle paused, clearing drift detection")
		err := r.DriftDetect.Clear(req.String())

		return ctrl.Result{}, err
	}

	merr := []error{}

	// retrieve the resources from the helm history.
	// if we can't retrieve the resources, we don't need to try any of the other operations and requeue now
	resources, err := r.Deployer.Resources(bd.Name, bd.Status.Release)
	if err != nil {
		logger.V(1).Info("Failed to retrieve bundledeployment's resources")
		return ctrl.Result{}, err
	}

	// return early if the bundledeployment is still being installed
	if !monitor.ShouldUpdateStatus(bd) {
		logger.V(1).Info("BundleDeployment is still being installed")
		return ctrl.Result{}, nil
	}

	// update the bundledeployment status from the helm resource list
	bd.Status, err = r.Monitor.UpdateStatus(ctx, bd, resources)
	if err != nil {
		logger.Error(err, "Cannot monitor deployed bundle")
	}

	// run drift correction
	if len(bd.Status.ModifiedStatus) > 0 && bd.Spec.CorrectDrift != nil && bd.Spec.CorrectDrift.Enabled {
		logger.V(1).Info("Removing external changes")
		if release, err := r.Deployer.RemoveExternalChanges(ctx, bd); err != nil {
			merr = append(merr, fmt.Errorf("failed reconciling drift: %w", err))
			// Propagate drift correction error to bundle deployment status.
			condition.Cond(fleetv1.BundleDeploymentConditionReady).SetError(&bd.Status, "", err)
		} else {
			bd.Status.Release = release
		}
	}

	// final status update
	statusPatch := client.MergeFrom(orig)
	err = r.Status().Patch(ctx, bd, statusPatch)
	if apierrors.IsNotFound(err) {
		merr = append(merr, fmt.Errorf("bundledeployment has been deleted: %w", err))
	} else if err != nil {
		merr = append(merr, fmt.Errorf("failed final update to bundledeployment status: %w", err))
	} else {
		logger.V(1).Info("Reconcile finished, bundledeployment status updated", "statusPatch", statusPatch)
	}

	return ctrl.Result{}, errutil.NewAggregate(merr)
}

// enqueueRequestHandlerWithDelay implements a TypedEventHandler that introduces a constant delay in the resources being enqueued
// Due to how workqueue.TypedDelayingInterface's AddAfter is implemented, successive calls with the same key are aggregated.
// Only implemented for Generic events, as this is only meant to be used from with source.Channel to receive internal events, not from an informer
func enqueueRequestHandlerWithDelay(delay time.Duration) handler.TypedEventHandler[*fleetv1.BundleDeployment, reconcile.Request] {
	return &handler.TypedFuncs[*fleetv1.BundleDeployment, reconcile.Request]{
		GenericFunc: func(ctx context.Context, e event.TypedGenericEvent[*fleetv1.BundleDeployment], w workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			if e.Object == nil {
				log.FromContext(ctx).Error(nil, "GenericEvent received with no metadata", "event", e)
				return
			}
			w.AddAfter(reconcile.Request{NamespacedName: types.NamespacedName{
				Name:      e.Object.GetName(),
				Namespace: e.Object.GetNamespace(),
			}}, delay)
		},
	}

}
