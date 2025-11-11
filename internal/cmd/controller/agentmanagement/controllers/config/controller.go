// Package config reads the initial global configuration.
package config

import (
	"context"

	"github.com/rancher/fleet/internal/config"

	corecontrollers "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"

	v1 "k8s.io/api/core/v1"
)

// Register watches for changes in the config. Both fleetcontroller and agentmanagement needs to register this since it
// is used in both programs.
func Register(ctx context.Context,
	namespace string,
	cm corecontrollers.ConfigMapController) error {

	cm.OnChange(ctx, "global-config", func(_ string, configMap *v1.ConfigMap) (*v1.ConfigMap, error) {
		return reloadConfig(namespace, configMap)
	})

	cfg, err := config.Lookup(ctx, namespace, config.ManagerConfigName, cm)
	if err != nil {
		return err
	}

	return config.SetAndTrigger(cfg)
}

func reloadConfig(namespace string, configMap *v1.ConfigMap) (*v1.ConfigMap, error) {
	if configMap == nil {
		// ConfigMap was deleted, nothing to reload. This is a normal deletion event.
		return nil, nil
	}

	if configMap.Name != config.ManagerConfigName ||
		configMap.Namespace != namespace {
		return configMap, nil
	}

	cfg, err := config.ReadConfig(configMap)
	if err != nil {
		return configMap, err
	}

	return configMap, config.SetAndTrigger(cfg)
}
