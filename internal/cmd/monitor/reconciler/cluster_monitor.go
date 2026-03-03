// Copyright (c) 2024-2026 SUSE LLC

package reconciler

import (
	"context"
	"reflect"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// ClusterMonitorReconciler monitors Cluster reconciliations
type ClusterMonitorReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	ShardID string
	Workers int

	// Cache to store previous state
	cache *ObjectCache

	// Per-controller logging mode
	DetailedLogs   bool
	EventFilters   EventTypeFilters
	ResourceFilter *ResourceFilter
}

// SetupWithManager sets up the controller with the Manager - IDENTICAL to ClusterReconciler.SetupWithManager
func (r *ClusterMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Initialize cache
	r.cache = NewObjectCache()

	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.Cluster{}).
		// Watch bundledeployments so we can update the status fields
		Watches(
			&fleet.BundleDeployment{},
			handler.EnqueueRequestsFromMapFunc(r.mapBundleDeploymentToCluster),
			builder.WithPredicates(predicate.Funcs{
				CreateFunc: func(e event.CreateEvent) bool {
					return true
				},
				// Triggering on every update would run into an
				// endless loop with the agentmanagement
				// cluster controller.
				// We still need to update often enough to keep the
				// status fields up to date.
				UpdateFunc: func(e event.UpdateEvent) bool {
					n := e.ObjectNew.(*fleet.BundleDeployment)
					o := e.ObjectOld.(*fleet.BundleDeployment)
					if n == nil || o == nil {
						return false
					}
					if !reflect.DeepEqual(n.Spec, o.Spec) {
						return true
					}
					if n.Status.AppliedDeploymentID != o.Status.AppliedDeploymentID {
						return true
					}
					if n.Status.Ready != o.Status.Ready {
						return true
					}
					return false
				},
				DeleteFunc: func(e event.DeleteEvent) bool {
					o := e.Object.(*fleet.BundleDeployment)
					if o == nil || o.Status.AppliedDeploymentID == "" {
						return false
					}
					return true
				},
			}),
		).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)
}

// Reconcile monitors cluster reconciliation events (read-only)
func (r *ClusterMonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Check resource filter - skip if resource doesn't match
	if !r.ResourceFilter.Matches(req.Namespace, req.Name) {
		return ctrl.Result{}, nil
	}

	logger := log.FromContext(ctx).WithName("cluster-monitor")
	logger = logger.WithValues(
		"cluster", req.NamespacedName.String(),
	)
	ctx = log.IntoContext(ctx, logger)

	cluster := &fleet.Cluster{}
	err := r.Get(ctx, req.NamespacedName, cluster)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logNotFound(logger, r.DetailedLogs, r.EventFilters, "Cluster", req.Namespace, req.Name)
			r.cache.Delete(req.NamespacedName)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check for deletion
	if !cluster.DeletionTimestamp.IsZero() {
		logDeletion(logger, r.DetailedLogs, r.EventFilters, "Cluster", cluster.Namespace, cluster.Name, cluster.DeletionTimestamp.String())
		r.cache.Delete(req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Retrieve old object from cache
	oldCluster, exists := r.cache.Get(req.NamespacedName)
	if !exists {
		logCreate(logger, r.DetailedLogs, r.EventFilters, "Cluster", cluster.Namespace, cluster.Name, cluster.Generation, cluster.ResourceVersion)
		r.cache.Set(req.NamespacedName, cluster.DeepCopy())
		return ctrl.Result{}, nil
	}

	oldClusterTyped := oldCluster.(*fleet.Cluster)

	// Detect what changed
	logSpecChange(logger, r.DetailedLogs, r.EventFilters, "Cluster", cluster.Namespace, cluster.Name, oldClusterTyped.Spec, cluster.Spec, oldClusterTyped.Generation, cluster.Generation)
	logStatusChange(logger, r.DetailedLogs, r.EventFilters, "Cluster", cluster.Namespace, cluster.Name, oldClusterTyped.Status, cluster.Status)
	logResourceVersionChangeWithMetadata(logger, r.DetailedLogs, r.EventFilters, "Cluster", cluster.Namespace, cluster.Name, oldClusterTyped, cluster)
	logAnnotationChange(logger, r.DetailedLogs, r.EventFilters, "Cluster", cluster.Namespace, cluster.Name, oldClusterTyped.Annotations, cluster.Annotations)
	logLabelChange(logger, r.DetailedLogs, r.EventFilters, "Cluster", cluster.Namespace, cluster.Name, oldClusterTyped.Labels, cluster.Labels)

	// Update cache with new state
	r.cache.Set(req.NamespacedName, cluster.DeepCopy())

	return ctrl.Result{}, nil
}

// mapBundleDeploymentToCluster maps BundleDeployment to Cluster - identical to cluster_controller.go
func (r *ClusterMonitorReconciler) mapBundleDeploymentToCluster(ctx context.Context, a client.Object) []ctrl.Request {
	clusterNS := &corev1.Namespace{}
	err := r.Get(ctx, types.NamespacedName{Name: a.GetNamespace()}, clusterNS)
	if err != nil {
		return nil
	}

	ns := clusterNS.Annotations[fleet.ClusterNamespaceAnnotation]
	name := clusterNS.Annotations[fleet.ClusterAnnotation]
	if ns == "" || name == "" {
		return nil
	}

	// Check resource filter before logging
	if !r.ResourceFilter.Matches(ns, name) {
		return nil
	}

	// Log trigger source
	logger := log.FromContext(ctx).WithName("cluster-monitor-handler")
	logRelatedResourceTrigger(logger, r.DetailedLogs, r.EventFilters, "Cluster", ns, name, "BundleDeployment", a.GetName(), a.GetNamespace())

	return []ctrl.Request{{
		NamespacedName: types.NamespacedName{
			Namespace: ns,
			Name:      name,
		},
	}}
}
