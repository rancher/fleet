// Copyright (c) 2021-2023 SUSE LLC

package reconciler

import (
	"context"
	"fmt"
	"reflect"

	"github.com/rancher/fleet/internal/cmd/controller/bundleevents"
	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/internal/metrics"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/sharding"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// BundleDeploymentReconciler reconciles a BundleDeployment object
type BundleDeploymentReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	ShardID string

	// Events reports the deployment state of this bundle deployment, if
	// per-deployment reporting is enabled. It may be nil, in which case no
	// such events are created.
	Events bundleevents.Notifier

	Workers int
}

// SetupWithManager sets up the controller with the Manager.
func (r *BundleDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.BundleDeployment{}, builder.WithPredicates(
			bundleDeploymentStatusChangedPredicate(),
		)).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)
}

//+kubebuilder:rbac:groups=fleet.cattle.io,resources=bundledeployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=bundledeployments/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=bundledeployments/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *BundleDeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("bundledeployment")

	bd := &fleet.BundleDeployment{}
	err := r.Get(ctx, req.NamespacedName, bd)

	if err != nil {
		metrics.BundleDeploymentCollector.Delete(req.Name, req.Namespace)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Enrich logger with userID if present
	if userID := bd.Labels[fleet.CreatedByUserIDLabel]; userID != "" {
		logger = logger.WithValues("userID", userID)
	}
	logger.V(1).Info("Reconciling bundledeployment")

	// The bundle reconciler takes care of adding the finalizer when creating a bundle deployment
	if !bd.DeletionTimestamp.IsZero() {
		// The state of a bundle deployment which is going away is not
		// worth reporting any more.
		if r.Events != nil {
			r.Events.Forget(bd.UID)
		}

		if controllerutil.ContainsFinalizer(bd, finalize.BundleDeploymentFinalizer) {
			err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
				err := r.Get(ctx, req.NamespacedName, bd)
				if err != nil {
					return client.IgnoreNotFound(err)
				}

				controllerutil.RemoveFinalizer(bd, finalize.BundleDeploymentFinalizer)

				return r.Update(ctx, bd)
			})
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from bundledeployment %s: %w", bd.Name, err)
			}
		}

		return ctrl.Result{}, nil
	}

	// increased log level, this triggers a lot
	logger.V(4).Info("Reconciling bundledeployment, updating display status field", "oldDisplay", bd.Status.Display)

	orig := bd.DeepCopy()

	var (
		deployed, monitored string
	)

	for _, cond := range bd.Status.Conditions {
		switch cond.Type {
		case "Deployed":
			deployed = conditionToMessage(cond)
		case "Monitored":
			monitored = conditionToMessage(cond)
		}
	}

	bd.Status.Display = fleet.BundleDeploymentDisplay{
		Deployed:  deployed,
		Monitored: monitored,
		State:     string(summary.GetDeploymentState(bd)),
	}

	// final update to bd
	statusPatch := client.MergeFrom(orig)
	if patchData, err := statusPatch.Data(bd); err == nil && string(patchData) == "{}" {
		// skip update if patch is empty
		return ctrl.Result{}, nil
	}
	patchErr := r.Status().Patch(ctx, bd, statusPatch)
	if client.IgnoreNotFound(patchErr) != nil {
		logger.V(1).Info("Reconcile failed update to bundledeployment status, requeuing", "status", bd.Status, "error", patchErr)
		// Use explicit requeue timing instead of error backoff for predictable retry behavior
		// Status patch failures are often transient (conflicts) and don't need exponential backoff
		//nolint:nilerr // Intentionally using fixed requeue interval instead of error backoff
		return ctrl.Result{RequeueAfter: durations.DefaultRequeueAfter}, nil
	}

	// The display state is only reported once it is persisted, and against
	// the previously persisted one, so that the state change is detected even
	// if the controller restarted in between. A NotFound patch persisted
	// nothing: the bundle deployment was deleted while it was reconciled, so
	// there is no state change to report.
	if r.Events != nil && patchErr == nil {
		r.Events.ObserveBundleDeployment(ctx, orig, bd)
	}

	metrics.BundleDeploymentCollector.Collect(ctx, bd)

	return ctrl.Result{}, nil
}

// bundleDeploymentStatusChangedPredicate returns true if the bundledeployment
// status has changed, or the bundledeployment was created
func bundleDeploymentStatusChangedPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool { return true },
		UpdateFunc: func(e event.UpdateEvent) bool {
			n, nOK := e.ObjectNew.(*fleet.BundleDeployment)
			o, oOK := e.ObjectOld.(*fleet.BundleDeployment)
			if !nOK || !oOK {
				return false
			}
			return !n.DeletionTimestamp.IsZero() || !reflect.DeepEqual(n.Status, o.Status)
		},
		GenericFunc: func(e event.GenericEvent) bool { return false },
	}
}

func conditionToMessage(cond genericcondition.GenericCondition) string {
	if cond.Reason == "Error" {
		return "Error: " + cond.Message
	}
	return string(cond.Status)
}
