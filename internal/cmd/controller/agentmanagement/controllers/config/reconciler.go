// Package config reads the initial global configuration.
package config

import (
	"context"

	"github.com/rancher/fleet/internal/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// ConfigReconciler reconciles the Fleet config object for agentmanagement,
// by reloading the config on change.
type ConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	SystemNamespace string
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		WithEventFilter(
			predicate.And(
				predicate.NewPredicateFuncs(func(object client.Object) bool {
					return object.GetNamespace() == r.SystemNamespace &&
						object.GetName() == config.ManagerConfigName
				}),
				predicate.Or(
					predicate.ResourceVersionChangedPredicate{},
					predicate.GenerationChangedPredicate{},
					predicate.AnnotationChangedPredicate{},
					predicate.LabelChangedPredicate{},
				),
			),
		).
		Complete(r)
}

// Reconcile reloads the Fleet config from the ConfigMap when it changes.
func (r *ConfigReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("agentmanagement-config")
	ctx = log.IntoContext(ctx, logger)

	cm := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Namespace: r.SystemNamespace, Name: config.ManagerConfigName}, cm)
	if client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, err
	}

	logger.V(1).Info("Reconciling config configmap, loading config")

	cfg, err := config.ReadConfig(cm)
	if err != nil {
		return ctrl.Result{}, err
	}

	// SetAndTrigger is used during the wrangler-to-CR migration to ensure
	// wrangler components (bootstrap, cluster/import) that register config.OnChange
	// callbacks still receive config change notifications.
	// TODO: Switch to config.Set() once those wrangler components are ported (Phases 3, 8).
	return ctrl.Result{}, config.SetAndTrigger(cfg)
}
