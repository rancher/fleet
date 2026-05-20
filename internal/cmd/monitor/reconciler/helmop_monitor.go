// Copyright (c) 2024-2026 SUSE LLC

package reconciler

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"
)

// HelmOpMonitorReconciler monitors HelmOp reconciliations
type HelmOpMonitorReconciler struct {
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

// SetupWithManager sets up the controller - mirrors HelmOpReconciler.SetupWithManager
func (r *HelmOpMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.cache = NewObjectCache()

	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.HelmOp{},
			builder.WithPredicates(
				predicate.Or(
					// Note: These predicates prevent cache
					// syncPeriod from triggering reconcile, since
					// cache sync is an Update event.
					predicate.GenerationChangedPredicate{},
					predicate.AnnotationChangedPredicate{},
					predicate.LabelChangedPredicate{},
				),
			),
		).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)
}

// Reconcile monitors HelmOp reconciliation events
func (r *HelmOpMonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Check resource filter - skip if resource doesn't match
	if !r.ResourceFilter.Matches(req.Namespace, req.Name) {
		return ctrl.Result{}, nil
	}

	logger := log.FromContext(ctx).WithName("helmop-monitor")
	logger = logger.WithValues(
		"helmop", req.String(),
	)
	ctx = log.IntoContext(ctx, logger)

	helmop := &fleet.HelmOp{}
	if err := r.Get(ctx, req.NamespacedName, helmop); err != nil {
		if client.IgnoreNotFound(err) == nil {
			logNotFound(logger, r.DetailedLogs, r.EventFilters, "HelmOp", req.Namespace, req.Name)
			r.cache.Delete(req.NamespacedName)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Add chart context if available
	if helmop.Spec.Helm != nil && helmop.Spec.Helm.Chart != "" {
		logger = logger.WithValues(
			"chart", helmop.Spec.Helm.Chart,
			"version", helmop.Spec.Helm.Version,
		)
	}

	// Check for deletion
	if !helmop.DeletionTimestamp.IsZero() {
		logDeletion(logger, r.DetailedLogs, r.EventFilters, "HelmOp", helmop.Namespace, helmop.Name, helmop.DeletionTimestamp.String())
		r.cache.Delete(req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Retrieve old object from cache
	oldHelmOp, exists := r.cache.Get(req.NamespacedName)
	if !exists {
		logCreate(logger, r.DetailedLogs, r.EventFilters, "HelmOp", helmop.Namespace, helmop.Name, helmop.Generation, helmop.ResourceVersion)
		r.cache.Set(req.NamespacedName, helmop.DeepCopy())
		return ctrl.Result{}, nil
	}

	oldHelmOpTyped := oldHelmOp.(*fleet.HelmOp)

	// Detect what changed
	logSpecChange(logger, r.DetailedLogs, r.EventFilters, "HelmOp", helmop.Namespace, helmop.Name, oldHelmOpTyped.Spec, helmop.Spec, oldHelmOpTyped.Generation, helmop.Generation)
	logStatusChange(logger, r.DetailedLogs, r.EventFilters, "HelmOp", helmop.Namespace, helmop.Name, oldHelmOpTyped.Status, helmop.Status)
	logResourceVersionChangeWithMetadata(logger, r.DetailedLogs, r.EventFilters, "HelmOp", helmop.Namespace, helmop.Name, oldHelmOpTyped, helmop)
	logAnnotationChange(logger, r.DetailedLogs, r.EventFilters, "HelmOp", helmop.Namespace, helmop.Name, oldHelmOpTyped.Annotations, helmop.Annotations)
	logLabelChange(logger, r.DetailedLogs, r.EventFilters, "HelmOp", helmop.Namespace, helmop.Name, oldHelmOpTyped.Labels, helmop.Labels)

	// Update cache with new state
	r.cache.Set(req.NamespacedName, helmop.DeepCopy())

	return ctrl.Result{}, nil
}
