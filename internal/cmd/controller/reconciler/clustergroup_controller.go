// Copyright (c) 2021-2023 SUSE LLC

package reconciler

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	fleetutil "github.com/rancher/fleet/internal/cmd/controller/errorutil"
	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/internal/metrics"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"
	"github.com/rancher/wrangler/v3/pkg/condition"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// ClusterGroupReconciler reconciles a ClusterGroup object
type ClusterGroupReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	ShardID string

	Workers int
}

const MaxReportedNonReadyClusters = 10

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.ClusterGroup{}, builder.WithPredicates(
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
		)).
		Watches(
			// Fan out from cluster to clustergroup
			&fleet.Cluster{},
			handler.EnqueueRequestsFromMapFunc(r.mapClusterToClusterGroup),
		).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)
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
	logger.V(1).Info("Reconciling clustergroup, updating summary and display status field", "oldDisplay", group.Status.Display)

	clusters := &fleet.ClusterList{}
	if group.Spec.Selector != nil {
		selector, err := metav1.LabelSelectorAsSelector(group.Spec.Selector)
		if err != nil {
			logger.Error(err, "Failed to parse selector", "selector", group.Spec.Selector)
			return ctrl.Result{}, r.updateErrorStatus(ctx, req.NamespacedName, group.Status, err)
		}

		err = r.List(ctx, clusters, client.InNamespace(req.Namespace), client.MatchingLabelsSelector{Selector: selector})
		if err != nil {
			return ctrl.Result{}, r.updateErrorStatus(ctx, req.NamespacedName, group.Status, err)
		}
	}

	// update summary
	group.Status.Summary = fleet.BundleSummary{}
	group.Status.ResourceCounts = fleet.GitRepoResourceCounts{}
	group.Status.ClusterCount = 0
	group.Status.NonReadyClusterCount = 0
	group.Status.NonReadyClusters = nil

	sort.Slice(clusters.Items, func(i, j int) bool {
		return clusters.Items[i].Name < clusters.Items[j].Name
	})

	for _, cluster := range clusters.Items {
		summary.IncrementResourceCounts(&group.Status.ResourceCounts, cluster.Status.ResourceCounts)
		summary.Increment(&group.Status.Summary, cluster.Status.Summary)
		group.Status.ClusterCount++
		if !summary.IsReady(cluster.Status.Summary) {
			group.Status.NonReadyClusterCount++
			if len(group.Status.NonReadyClusters) < MaxReportedNonReadyClusters {
				group.Status.NonReadyClusters = append(group.Status.NonReadyClusters, cluster.Name)
			}
		}
	}

	summary.SetReadyConditions(&group.Status, "Bundle", group.Status.Summary)

	// update display
	group.Status.Display.ReadyBundles = fmt.Sprintf("%d/%d", group.Status.Summary.Ready, group.Status.Summary.DesiredReady)
	group.Status.Display.ReadyClusters = fmt.Sprintf("%d/%d", group.Status.ClusterCount-group.Status.NonReadyClusterCount, group.Status.ClusterCount)
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

	r.setCondition(&group.Status, nil)

	err = r.updateStatus(ctx, req.NamespacedName, group.Status)
	if err != nil {
		logger.V(1).Info("Reconcile failed final update to cluster group status", "status", group.Status, "error", err)
	} else {
		metrics.ClusterGroupCollector.Collect(ctx, group)
	}

	return ctrl.Result{}, err
}

// setCondition sets the condition and updates the timestamp, if the condition changed
func (r *ClusterGroupReconciler) setCondition(status *fleet.ClusterGroupStatus, err error) {
	cond := condition.Cond(fleet.ClusterGroupConditionProcessed)
	origStatus := status.DeepCopy()
	cond.SetError(status, "", fleetutil.IgnoreConflict(err))
	if !equality.Semantic.DeepEqual(origStatus, status) {
		cond.LastUpdated(status, time.Now().UTC().Format(time.RFC3339))
	}
}

func (r *ClusterGroupReconciler) updateErrorStatus(ctx context.Context, req types.NamespacedName, status fleet.ClusterGroupStatus, orgErr error) error {
	r.setCondition(&status, orgErr)
	if statusErr := r.updateStatus(ctx, req, status); statusErr != nil {
		merr := []error{orgErr, fmt.Errorf("failed to update the status: %w", statusErr)}
		return errutil.NewAggregate(merr)
	}
	return orgErr
}

func (r *ClusterGroupReconciler) updateStatus(ctx context.Context, req types.NamespacedName, status fleet.ClusterGroupStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.ClusterGroup{}
		err := r.Get(ctx, req, t)
		if err != nil {
			return err
		}
		t.Status = status
		return r.Status().Update(ctx, t)
	})
}

func (r *ClusterGroupReconciler) mapClusterToClusterGroup(ctx context.Context, a client.Object) []ctrl.Request {
	ns := a.GetNamespace()
	logger := log.FromContext(ctx).WithName("clustergroup-cluster-handler").WithValues("namespace", ns)
	cluster := a.(*fleet.Cluster)

	cgs := &fleet.ClusterGroupList{}
	err := r.List(ctx, cgs, client.InNamespace(ns))
	if err != nil {
		logger.Error(err, "Failed to list cluster groups in namespace")
		return nil
	}

	// avoid log message if no clustergroups are found, no fan-out needed
	if len(cgs.Items) == 0 {
		return nil
	}
	logger.Info("Cluster changed, enqueue matching cluster groups", "name", cluster.GetName())

	requests := []ctrl.Request{}
	for _, cg := range cgs.Items {
		if cg.Spec.Selector == nil {
			// clustergroup does not match any clusters
			continue
		}

		sel, err := metav1.LabelSelectorAsSelector(cg.Spec.Selector)
		if err != nil {
			logger.Error(err, "invalid selector on clustergroup", "selector", sel, "name", cg.GetName())
			continue
		}

		if sel.Matches(labels.Set(cluster.Labels)) {
			requests = append(requests, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: ns,
					Name:      cg.Name,
				},
			})
			// only need to enqueue this cluster group once, no need to check cluster count
			continue
		}

		// if cluster is removed from CG, need to reconcile if ClusterCount doesnt match
		clusters := &fleet.ClusterList{}
		err = r.List(ctx, clusters, client.InNamespace(ns), &client.ListOptions{LabelSelector: sel})
		if err != nil {
			// non-fatal error, just log and continue
			logger.Error(err, "error fetching clusters in clustergroup", "name", cg.GetName())
		}
		if cg.Status.ClusterCount != len(clusters.Items) {
			requests = append(requests, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: ns,
					Name:      cg.Name,
				},
			})
		}
	}

	return requests
}
