// Copyright (c) 2024-2026 SUSE LLC

package reconciler

import (
	"context"
	"fmt"
	"strings"

	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// BundleMonitorReconciler monitors Bundle reconciliations
type BundleMonitorReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	ShardID string
	Workers int

	// BundleQuery for cluster->bundle mapping
	Query BundleQuery

	// Cache to store previous state
	cache *ObjectCache

	// Per-controller logging mode
	DetailedLogs   bool
	EventFilters   EventTypeFilters
	ResourceFilter *ResourceFilter
}

// SetupWithManager sets up the controller - mirrors BundleReconciler.SetupWithManager
func (r *BundleMonitorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.cache = NewObjectCache()

	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.Bundle{},
			builder.WithPredicates(
				// do not trigger for bundle status changes (except for cache sync)
				predicate.Or(
					TypedResourceVersionUnchangedPredicate[client.Object]{},
					predicate.GenerationChangedPredicate{},
					predicate.AnnotationChangedPredicate{},
					predicate.LabelChangedPredicate{},
				),
			),
		).
		// Note: Maybe improve with WatchesMetadata, does it have access to labels?
		Watches(
			// Fan out from bundledeployment to bundle
			&fleet.BundleDeployment{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, a client.Object) []ctrl.Request {
				bd := a.(*fleet.BundleDeployment)
				labels := bd.GetLabels()
				if labels == nil {
					return nil
				}

				ns, name := target.BundleFromDeployment(labels)
				if ns != "" && name != "" {
					// Check resource filter before logging
					if r.ResourceFilter.Matches(ns, name) {
						// Log trigger source
						logger := log.FromContext(ctx)
						logRelatedResourceTrigger(logger, r.DetailedLogs, r.EventFilters, "Bundle", ns, name, "BundleDeployment", a.GetName(), a.GetNamespace())

						return []ctrl.Request{{
							NamespacedName: types.NamespacedName{
								Namespace: ns,
								Name:      name,
							},
						}}
					}
				}

				return nil
			}),
			builder.WithPredicates(bundleDeploymentStatusChangedPredicate()),
		).
		Watches(
			// Fan out from cluster to bundle
			&fleet.Cluster{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, a client.Object) []ctrl.Request {
				cluster := a.(*fleet.Cluster)
				logger := log.FromContext(ctx)

				// Query which bundles are affected by this cluster
				bundlesToRefresh, _, err := r.Query.BundlesForCluster(ctx, cluster)
				if err != nil {
					// Log error but don't fail - monitoring shouldn't crash on query errors
					logger.Error(err, "Failed to query bundles for cluster",
						"cluster", cluster.Name,
						"namespace", cluster.Namespace)
					return nil
				}

				requests := []ctrl.Request{}
				for _, bundle := range bundlesToRefresh {
					if !sharding.ShouldProcess(bundle, r.ShardID) {
						continue
					}
					// Check resource filter before logging and enqueueing
					if r.ResourceFilter.Matches(bundle.Namespace, bundle.Name) {
						// Log each bundle trigger with correct name/namespace
						logRelatedResourceTrigger(logger, r.DetailedLogs, r.EventFilters,
							"Bundle", bundle.Namespace, bundle.Name,
							"Cluster", cluster.GetName(), cluster.GetNamespace())

						requests = append(requests, ctrl.Request{
							NamespacedName: types.NamespacedName{
								Namespace: bundle.Namespace,
								Name:      bundle.Name,
							},
						})
					}
				}

				return requests
			}),
			builder.WithPredicates(clusterChangedPredicate()),
		).
		Watches(
			// Fan out from secret to bundle, reconcile bundles when a secret
			// referenced in DownstreamResources changes.
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.downstreamResourceMapFunc("Secret")),
			builder.WithPredicates(dataChangedPredicate()),
		).
		Watches(
			// Fan out from configmap to bundle, reconcile bundles when a configmap
			// referenced in DownstreamResources changes.
			&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(r.downstreamResourceMapFunc("ConfigMap")),
			builder.WithPredicates(dataChangedPredicate()),
		).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)
}

// downstreamResourceMapFunc returns a function that maps a Secret or ConfigMap to Bundles
// that reference it in their DownstreamResources.
func (r *BundleMonitorReconciler) downstreamResourceMapFunc(kind string) func(ctx context.Context, obj client.Object) []ctrl.Request {
	lowerKind := strings.ToLower(kind)

	return func(ctx context.Context, obj client.Object) []ctrl.Request {
		// Create the index key for this resource (Kind/Name)
		indexKey := fmt.Sprintf("%s/%s", lowerKind, obj.GetName())

		// Find all bundles that reference this resource
		bundleList := &fleet.BundleList{}
		err := r.List(ctx, bundleList,
			client.InNamespace(obj.GetNamespace()),
			client.MatchingFields{config.BundleDownstreamResourceIndex: indexKey},
		)
		if err != nil {
			return nil
		}

		requests := make([]ctrl.Request, 0, len(bundleList.Items))
		for _, bundle := range bundleList.Items {
			if !sharding.ShouldProcess(&bundle, r.ShardID) {
				continue
			}
			if !r.ResourceFilter.Matches(bundle.Namespace, bundle.Name) {
				continue
			}
			requests = append(requests, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: bundle.Namespace,
					Name:      bundle.Name,
				},
			})
		}

		return requests
	}
}

// Reconcile monitors bundle reconciliation events
func (r *BundleMonitorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Check resource filter - skip if resource doesn't match
	if !r.ResourceFilter.Matches(req.Namespace, req.Name) {
		return ctrl.Result{}, nil
	}

	logger := log.FromContext(ctx).WithName("bundle-monitor")
	logger = logger.WithValues(
		"bundle", req.NamespacedName.String(),
		"mode", LogMode(r.DetailedLogs),
	)
	ctx = log.IntoContext(ctx, logger)

	bundle := &fleet.Bundle{}
	if err := r.Get(ctx, req.NamespacedName, bundle); err != nil {
		if client.IgnoreNotFound(err) == nil {
			logNotFound(logger, r.DetailedLogs, r.EventFilters, "Bundle", req.Namespace, req.Name)
			r.cache.Delete(req.NamespacedName)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Add gitrepo context if available
	if bundle.Labels[fleet.RepoLabel] != "" {
		logger = logger.WithValues(
			"gitrepo", bundle.Labels[fleet.RepoLabel],
			"commit", bundle.Labels[fleet.CommitLabel],
		)
	}

	// Check for deletion
	if !bundle.DeletionTimestamp.IsZero() {
		logDeletion(logger, r.DetailedLogs, r.EventFilters, "Bundle", bundle.Namespace, bundle.Name, bundle.DeletionTimestamp.String())
		r.cache.Delete(req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Retrieve old object from cache
	oldBundle, exists := r.cache.Get(req.NamespacedName)
	if !exists {
		logCreate(logger, r.DetailedLogs, r.EventFilters, "Bundle", bundle.Namespace, bundle.Name, bundle.Generation, bundle.ResourceVersion)
		r.cache.Set(req.NamespacedName, bundle.DeepCopy())
		return ctrl.Result{}, nil
	}

	oldBundleTyped := oldBundle.(*fleet.Bundle)

	// Detect what changed - pass DetailedLogs flag
	logSpecChange(logger, r.DetailedLogs, r.EventFilters, "Bundle", bundle.Namespace, bundle.Name, oldBundleTyped.Spec, bundle.Spec, oldBundleTyped.Generation, bundle.Generation)
	logStatusChange(logger, r.DetailedLogs, r.EventFilters, "Bundle", bundle.Namespace, bundle.Name, oldBundleTyped.Status, bundle.Status)
	logResourceVersionChangeWithMetadata(logger, r.DetailedLogs, r.EventFilters, "Bundle", bundle.Namespace, bundle.Name, oldBundleTyped, bundle)
	logAnnotationChange(logger, r.DetailedLogs, r.EventFilters, "Bundle", bundle.Namespace, bundle.Name, oldBundleTyped.Annotations, bundle.Annotations)
	logLabelChange(logger, r.DetailedLogs, r.EventFilters, "Bundle", bundle.Namespace, bundle.Name, oldBundleTyped.Labels, bundle.Labels)

	// Update cache with new state
	r.cache.Set(req.NamespacedName, bundle.DeepCopy())

	return ctrl.Result{}, nil
}

// LogMode returns "detailed" or "summary" based on the flag.
func LogMode(detailed bool) string {
	if detailed {
		return "detailed"
	}
	return "summary"
}
