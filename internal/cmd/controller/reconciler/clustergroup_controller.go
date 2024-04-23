// Copyright (c) 2021-2023 SUSE LLC

package reconciler

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/rancher/fleet/internal/metrics"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// ClusterGroupReconciler reconciles a ClusterGroup object
type ClusterGroupReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	ShardID string
}

//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clustergroups,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clustergroups/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clustergroups/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ClusterGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("clustergroup")

	group := &fleet.ClusterGroup{}
	err := r.Get(ctx, req.NamespacedName, group)
	if err != nil {
		metrics.ClusterGroupCollector.Delete(req.Name, req.Namespace)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	logger.V(1).Info("Reconciling clustergroup, updating display status field", "oldDisplay", group.Status.Display)

	group.Status.Display.ReadyBundles = fmt.Sprintf("%d/%d",
		group.Status.Summary.Ready,
		group.Status.Summary.DesiredReady)
	group.Status.Display.ReadyClusters = fmt.Sprintf("%d/%d",
		group.Status.ClusterCount-group.Status.NonReadyClusterCount,
		group.Status.ClusterCount)
	if len(group.Status.NonReadyClusters) > 0 {
		group.Status.Display.ReadyClusters += " (" + strings.Join(group.Status.NonReadyClusters, ",") + ")"
	}

	var state fleet.BundleState
	for _, nonReady := range group.Status.Summary.NonReadyResources {
		if fleet.StateRank[nonReady.State] > fleet.StateRank[state] {
			state = nonReady.State
		}
	}

	group.Status.Display.State = string(state)

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.ClusterGroup{}
		err := r.Get(ctx, req.NamespacedName, t)
		if err != nil {
			return err
		}
		t.Status = group.Status
		return r.Status().Update(ctx, t)
	})
	if err != nil {
		logger.V(1).Error(err, "Reconcile failed final update to cluster group status", "status", group.Status)
	} else {
		metrics.ClusterGroupCollector.Collect(ctx, group)
	}

	return ctrl.Result{}, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.ClusterGroup{}).
		WithEventFilter(
			predicate.And(
				sharding.FilterByShardID(r.ShardID),
				// only trigger on status changes, create
				predicate.Funcs{
					CreateFunc: func(e event.CreateEvent) bool {
						return true
					},
					UpdateFunc: func(e event.UpdateEvent) bool {
						n := e.ObjectNew.(*fleet.ClusterGroup)
						o := e.ObjectOld.(*fleet.ClusterGroup)
						if n == nil || o == nil {
							return false
						}
						return !reflect.DeepEqual(n.Status, o.Status)
					},
				},
			),
		).
		Complete(r)
}
