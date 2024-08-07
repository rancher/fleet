// Package config reads the initial global configuration.
package reconciler

import (
	"context"

	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/pkg/sharding"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// ConfigReconciler reconciles the Fleet config object, by
// reloading the config on change.
type ConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	SystemNamespace string
	ShardID         string
}

// Load the config from the configmap and set it in the config package.
func Load(ctx context.Context, c client.Reader, namespace string) error {
	cm := &corev1.ConfigMap{}
	err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: config.ManagerConfigName}, cm)
	// use an empty config if the configmap is not found
	if client.IgnoreNotFound(err) != nil {
		return err
	}

	cfg, err := config.ReadConfig(cm)
	if err != nil {
		return err
	}

	config.Set(cfg)
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// TODO Maybe we can limit this Watch to the system namespace?
		For(&corev1.ConfigMap{}).
		WithEventFilter(
			// we do not trigger for status changes
			predicate.And(
				sharding.FilterByShardID(r.ShardID),
				predicate.NewPredicateFuncs(func(object client.Object) bool {
					return object.GetNamespace() == r.SystemNamespace &&
						object.GetName() == config.ManagerConfigName
				}),
				predicate.Or(
					predicate.ResourceVersionChangedPredicate{},
					predicate.GenerationChangedPredicate{},
					predicate.AnnotationChangedPredicate{},
					predicate.LabelChangedPredicate{},
				))).
		Complete(r)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ConfigReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("config")
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

	config.Set(cfg)

	return ctrl.Result{}, nil
}
