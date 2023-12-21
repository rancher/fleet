// Copyright (c) 2021-2023 SUSE LLC

package reconciler

import (
	"context"
	"reflect"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v2/pkg/genericcondition"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// BundleDeploymentReconciler reconciles a BundleDeployment object
type BundleDeploymentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
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
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// increased log level, this triggers a lot
	logger.V(4).Info("Reconciling bundledeployment, updating display status field", "oldDisplay", bd.Status.Display)

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

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.BundleDeployment{}
		err := r.Get(ctx, req.NamespacedName, t)
		if err != nil {
			return err
		}
		t.Status = bd.Status
		return r.Status().Update(ctx, t)
	})
	if err != nil {
		logger.V(1).Error(err, "Reconcile failed final update to bundle deployment status", "status", bd.Status)
	}

	return ctrl.Result{}, err
}

// bundleDeploymentStatusChangedPredicate returns true if the bundledeployment
// status has changed, or the bundledeployment was created
func bundleDeploymentStatusChangedPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			n := e.ObjectNew.(*fleet.BundleDeployment)
			o := e.ObjectOld.(*fleet.BundleDeployment)
			if n == nil || o == nil {
				return false
			}
			return !reflect.DeepEqual(n.Status, o.Status)
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *BundleDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.BundleDeployment{}).
		WithEventFilter(bundleDeploymentStatusChangedPredicate()).
		Complete(r)
}

func conditionToMessage(cond genericcondition.GenericCondition) string {
	if cond.Reason == "Error" {
		return "Error: " + cond.Message
	}
	return string(cond.Status)
}
