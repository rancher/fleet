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
	"sigs.k8s.io/controller-runtime/pkg/source"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type HelmOpStatusReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Workers int
	ShardID string
}

func (r *HelmOpStatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.HelmOp{}).
		// Fan out from bundle to HelmOp
		WatchesRawSource(source.TypedKind(
			mgr.GetCache(),
			&fleet.Bundle{},
			handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, a *fleet.Bundle) []ctrl.Request {
				app := a.GetLabels()[fleet.HelmOpLabel]
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
			sharding.TypedFilterByShardID[*fleet.Bundle](r.ShardID), // WatchesRawSources ignores event filters, we need to use a predicate
			status.BundleStatusChangedPredicate(),
		)).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Named("HelmOpStatus").
		Complete(r)
}

// Reconcile reads the stat of the HelmOp and BundleDeployments and
// computes status fields for the HelmOp. This status is used to
// display information to the user.
func (r *HelmOpStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("helmop-status")
	helmop := &fleet.HelmOp{}

	if err := r.Get(ctx, req.NamespacedName, helmop); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	} else if errors.IsNotFound(err) {
		return ctrl.Result{}, nil
	}

	if !helmop.DeletionTimestamp.IsZero() {
		// the HelmOp controller will handle deletion
		return ctrl.Result{}, nil
	}

	if helmop.Spec.Helm.Chart == "" {
		return ctrl.Result{}, nil
	}

	logger = logger.WithValues("generation", helmop.Generation, "chart", helmop.Spec.Helm.Chart).WithValues("conditions", helmop.Status.Conditions)
	ctx = log.IntoContext(ctx, logger)

	logger.V(1).Info("Reconciling HelmOp status")

	bdList := &fleet.BundleDeploymentList{}
	err := r.List(ctx, bdList, client.MatchingLabels{
		fleet.HelmOpLabel:          helmop.Name,
		fleet.BundleNamespaceLabel: helmop.Namespace,
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	err = setStatusHelm(bdList, helmop)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.Client.Status().Update(ctx, helmop)
	if err != nil {
		logger.Error(err, "Reconcile failed update to HelmOp status", "status", helmop.Status)
		return ctrl.Result{RequeueAfter: durations.HelmOpStatusDelay}, nil
	}

	return ctrl.Result{}, nil
}

func setStatusHelm(list *fleet.BundleDeploymentList, helmop *fleet.HelmOp) error {
	// sort bundledeployments so lists in status are always in the same order
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].UID < list.Items[j].UID
	})

	err := status.SetFields(list, &helmop.Status.StatusBase)
	if err != nil {
		return err
	}

	resourcestatus.SetResources(list.Items, &helmop.Status.StatusBase)

	summary.SetReadyConditions(&helmop.Status, "Bundle", helmop.Status.Summary)

	helmop.Status.Display.ReadyBundleDeployments = fmt.Sprintf("%d/%d",
		helmop.Status.Summary.Ready,
		helmop.Status.Summary.DesiredReady)

	return nil
}
