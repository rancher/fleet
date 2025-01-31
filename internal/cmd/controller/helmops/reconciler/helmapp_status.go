package reconciler

import (
	"context"
	"fmt"
	"sort"

	"github.com/rancher/fleet/internal/cmd/controller/status"
	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/internal/resourcestatus"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/sharding"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type HelmAppStatusReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Workers int
	ShardID string
}

func (r *HelmAppStatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.HelmApp{}).
		Watches(
			// Fan out from bundle to HelmApp
			&fleet.Bundle{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, a client.Object) []ctrl.Request {
				app := a.GetLabels()[fleet.HelmAppLabel]
				if app != "" {
					return []ctrl.Request{{
						NamespacedName: types.NamespacedName{
							Namespace: a.GetNamespace(),
							Name:      app,
						},
					}}
				}

				return []ctrl.Request{}
			}),
			builder.WithPredicates(status.BundleStatusChangedPredicate()),
		).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Named("HelmAppStatus").
		Complete(r)
}

// Reconcile reads the stat of the HelmApp and BundleDeployments and
// computes status fields for the HelmApp. This status is used to
// display information to the user.
func (r *HelmAppStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if !experimentalHelmOpsEnabled() {
		return ctrl.Result{}, nil
	}
	logger := log.FromContext(ctx).WithName("helmapp-status")
	helmapp := &fleet.HelmApp{}

	if err := r.Get(ctx, req.NamespacedName, helmapp); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	} else if errors.IsNotFound(err) {
		return ctrl.Result{}, nil
	}

	if !helmapp.DeletionTimestamp.IsZero() {
		// the HelmApp controller will handle deletion
		return ctrl.Result{}, nil
	}

	if helmapp.Spec.Helm.Chart == "" {
		return ctrl.Result{}, nil
	}

	logger = logger.WithValues("generation", helmapp.Generation, "chart", helmapp.Spec.Helm.Chart).WithValues("conditions", helmapp.Status.Conditions)
	ctx = log.IntoContext(ctx, logger)

	logger.V(1).Info("Reconciling HelmApp status")

	bdList := &fleet.BundleDeploymentList{}
	err := r.List(ctx, bdList, client.MatchingLabels{
		fleet.HelmAppLabel:         helmapp.Name,
		fleet.BundleNamespaceLabel: helmapp.Namespace,
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	err = setStatusHelm(bdList, helmapp)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.Client.Status().Update(ctx, helmapp)
	if err != nil {
		logger.Error(err, "Reconcile failed update to helm app status", "status", helmapp.Status)
		return ctrl.Result{RequeueAfter: durations.HelmAppStatusDelay}, nil
	}

	return ctrl.Result{}, nil
}

func setStatusHelm(list *fleet.BundleDeploymentList, helmapp *fleet.HelmApp) error {
	// sort for resourceKey?
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].UID < list.Items[j].UID
	})

	err := status.SetFields(list, &helmapp.Status.StatusBase)
	if err != nil {
		return err
	}

	resourcestatus.SetResources(list.Items, &helmapp.Status.StatusBase)

	summary.SetReadyConditions(&helmapp.Status, "Bundle", helmapp.Status.Summary)

	helmapp.Status.Display.ReadyBundleDeployments = fmt.Sprintf("%d/%d",
		helmapp.Status.Summary.Ready,
		helmapp.Status.Summary.DesiredReady)

	return nil
}
