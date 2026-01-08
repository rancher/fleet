// Package config reads the initial global configuration.
package config

import (
	"context"

	"github.com/rancher/fleet/internal/config"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1 "k8s.io/api/core/v1"
)

// Register registers the config controller using controller-runtime.
func Register(ctx context.Context, mgr ctrl.Manager, namespace string) error {
	// First, load the initial config
	// We need a client to get the configmap
	cm := &v1.ConfigMap{}
	// Use the APIReader to avoid cache dependency before manager start
	if err := mgr.GetAPIReader().Get(ctx, client.ObjectKey{
		Namespace: namespace,
		Name:      config.ManagerConfigName,
	}, cm); err != nil {
		return err
	}

	cfg, err := config.ReadConfig(cm)
	if err != nil {
		return err
	}

	// Do not trigger callbacks before the manager starts; reconciler will handle triggering
	config.Set(cfg)

	// Set up the reconciler
	reconciler := &ConfigMapReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		SystemNamespace: namespace,
	}

	return reconciler.SetupWithManager(mgr)
}
