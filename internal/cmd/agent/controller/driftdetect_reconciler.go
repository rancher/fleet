package controller

import (
	"context"
	"fmt"

	"github.com/rancher/fleet/internal/cmd/agent/deployer"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/cleanup"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/driftdetect"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/monitor"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/condition"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type DriftDetectReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Deployer    *deployer.Deployer
	Monitor     *monitor.Monitor
	DriftDetect *driftdetect.DriftDetect
	Cleanup     *cleanup.Cleanup

	DriftChan chan event.GenericEvent
}

// SetupWithManager sets up the controller with the Manager.
func (r *DriftDetectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	src := source.Channel(r.DriftChan, &handler.EnqueueRequestForObject{})
	return ctrl.NewControllerManagedBy(mgr).
		Named("test").
		WatchesRawSource(src).
		Complete(r)

}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *DriftDetectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("bundledeployment")
	ctx = log.IntoContext(ctx, logger)

	// get latest BundleDeployment from cluster
	bd := &fleetv1.BundleDeployment{}
	err := r.Get(ctx, req.NamespacedName, bd)
	if apierrors.IsNotFound(err) {
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, err
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
		return ctrl.Result{}, nil
	}

	// update the bundledeployment status and check if we deploy an agent, or if we need to trigger drift correction
	status, err := r.Monitor.UpdateStatus(ctx, bd, resources)
	if err != nil {
		logger.Error(err, "Cannot monitor deployed bundle")
	}

	// Run drift correction
	if len(status.ModifiedStatus) > 0 && bd.Spec.CorrectDrift != nil && bd.Spec.CorrectDrift.Enabled {
		if release, err := r.Deployer.RemoveExternalChanges(ctx, bd); err != nil {
			merr = append(merr, fmt.Errorf("failed reconciling drift: %w", err))
			// Propagate drift correction error to bundle deployment status.
			condition.Cond(fleetv1.BundleDeploymentConditionReady).SetError(&status, "", err)
		} else {
			bd.Status.Release = release
		}
	}

	// final status update
	logger.V(1).Info("Reconcile finished, updating the bundledeployment status")
	err = r.updateStatus(ctx, req.NamespacedName, bd.Status)
	if apierrors.IsNotFound(err) {
		merr = append(merr, fmt.Errorf("bundledeployment has been deleted: %w", err))
	} else if err != nil {
		merr = append(merr, fmt.Errorf("failed final update to bundledeployment status: %w", err))
	}

	return ctrl.Result{}, errutil.NewAggregate(merr)
}

func (r *DriftDetectReconciler) updateStatus(ctx context.Context, req types.NamespacedName, status fleetv1.BundleDeploymentStatus) error {
	return retry.RetryOnConflict(DefaultRetry, func() error {
		newBD := &fleetv1.BundleDeployment{}
		err := r.Get(ctx, req, newBD)
		if err != nil {
			return err
		}
		newBD.Status = status
		return r.Status().Update(ctx, newBD)
	})
}
