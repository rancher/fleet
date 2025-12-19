// Package config reads the initial global configuration.
// This file contains the controller-runtime based reconciler for the config controller.
package config

import (
	"context"

	"github.com/rancher/fleet/internal/config"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// ConfigMapReconciler reconciles the fleet-controller ConfigMap to update global config
type ConfigMapReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	SystemNamespace string
}

// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

// Reconcile handles ConfigMap changes and updates the global fleet config
func (r *ConfigMapReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Only process the fleet-controller configmap in the system namespace
	if req.Name != config.ManagerConfigName || req.Namespace != r.SystemNamespace {
		return ctrl.Result{}, nil
	}

	logger.V(1).Info("Reconciling fleet config", "configmap", req.NamespacedName)

	var configMap corev1.ConfigMap
	if err := r.Get(ctx, req.NamespacedName, &configMap); err != nil {
		// ConfigMap was deleted or doesn't exist, nothing to do
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Parse and set the config
	cfg, err := config.ReadConfig(&configMap)
	if err != nil {
		logger.Error(err, "Failed to parse config from ConfigMap")
		return ctrl.Result{}, err
	}

	if err := config.SetAndTrigger(cfg); err != nil {
		logger.Error(err, "Failed to set and trigger config")
		return ctrl.Result{}, err
	}

	logger.Info("Successfully updated fleet config", "configmap", req.NamespacedName)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConfigMapReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		WithEventFilter(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			// Only watch the fleet-controller configmap in the system namespace
			return obj.GetName() == config.ManagerConfigName && obj.GetNamespace() == r.SystemNamespace
		})).
		Complete(r)
}
