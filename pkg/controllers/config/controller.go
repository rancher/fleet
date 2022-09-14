// Package config reads the initial global configuration. (fleetcontroller)
package config

import (
	"context"

	"github.com/rancher/fleet/pkg/config"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	v1 "k8s.io/api/core/v1"
)

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

	return config.Set(cfg)
}

func reloadConfig(namespace string, configMap *v1.ConfigMap) (*v1.ConfigMap, error) {
	if configMap == nil {
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

	return configMap, config.Set(cfg)
}
