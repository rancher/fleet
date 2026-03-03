// Copyright (c) 2024-2026 SUSE LLC

package reconciler

import (
	"context"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// BundleDeploymentMonitorReconciler monitors BundleDeployment reconciliations
type BundleDeploymentMonitorReconciler struct {
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

// SetupWithManager sets up the controller - IDENTICAL to BundleDeploymentReconciler.SetupWithManager
func (r *BundleDeploymentMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.cache = NewObjectCache()

	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.BundleDeployment{}, builder.WithPredicates(
			bundleDeploymentStatusChangedPredicate(),
		)).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)
}

// Reconcile monitors bundledeployment reconciliation events (READ-ONLY)
func (r *BundleDeploymentMonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Check resource filter - skip if resource doesn't match
	if !r.ResourceFilter.Matches(req.Namespace, req.Name) {
		return ctrl.Result{}, nil
	}

	logger := log.FromContext(ctx).WithName("bundledeployment-monitor")
	logger = logger.WithValues(
		"bundledeployment", req.NamespacedName.String(),
	)
	ctx = log.IntoContext(ctx, logger)

	bd := &fleet.BundleDeployment{}
	if err := r.Get(ctx, req.NamespacedName, bd); err != nil {
		if client.IgnoreNotFound(err) == nil {
			logNotFound(logger, r.DetailedLogs, r.EventFilters, "BundleDeployment", req.Namespace, req.Name)
			r.cache.Delete(req.NamespacedName)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Add bundle context if available from labels
	if bd.Labels != nil {
		bundleNS := bd.Labels["fleet.cattle.io/bundle-namespace"]
		bundleName := bd.Labels["fleet.cattle.io/bundle"]
		if bundleNS != "" && bundleName != "" {
			logger = logger.WithValues(
				"bundle", bundleNS+"/"+bundleName,
			)
		}
	}

	// Check for deletion
	if !bd.DeletionTimestamp.IsZero() {
		logDeletion(logger, r.DetailedLogs, r.EventFilters, "BundleDeployment", bd.Namespace, bd.Name, bd.DeletionTimestamp.String())
		r.cache.Delete(req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Retrieve old object from cache
	oldBD, exists := r.cache.Get(req.NamespacedName)
	if !exists {
		logCreate(logger, r.DetailedLogs, r.EventFilters, "BundleDeployment", bd.Namespace, bd.Name, bd.Generation, bd.ResourceVersion)
		r.cache.Set(req.NamespacedName, bd.DeepCopy())
		return ctrl.Result{}, nil
	}

	oldBDTyped := oldBD.(*fleet.BundleDeployment)

	// Detect what changed
	logSpecChange(logger, r.DetailedLogs, r.EventFilters, "BundleDeployment", bd.Namespace, bd.Name, oldBDTyped.Spec, bd.Spec, oldBDTyped.Generation, bd.Generation)
	logStatusChange(logger, r.DetailedLogs, r.EventFilters, "BundleDeployment", bd.Namespace, bd.Name, oldBDTyped.Status, bd.Status)
	logResourceVersionChangeWithMetadata(logger, r.DetailedLogs, r.EventFilters, "BundleDeployment", bd.Namespace, bd.Name, oldBDTyped, bd)
	logAnnotationChange(logger, r.DetailedLogs, r.EventFilters, "BundleDeployment", bd.Namespace, bd.Name, oldBDTyped.Annotations, bd.Annotations)
	logLabelChange(logger, r.DetailedLogs, r.EventFilters, "BundleDeployment", bd.Namespace, bd.Name, oldBDTyped.Labels, bd.Labels)

	// Update cache with new state
	r.cache.Set(req.NamespacedName, bd.DeepCopy())

	return ctrl.Result{}, nil
}
