package config

import (
	"context"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/config"
	corecontrollers "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/objectset"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Register(ctx context.Context,
	apply apply.Apply,
	cm corecontrollers.ConfigMapController) error {

	if err := setup(ctx, apply, cm); err != nil {
		return err
	}

	cm.OnChange(ctx, "global-config", func(_ string, configMap *v1.ConfigMap) (*v1.ConfigMap, error) {
		return reloadConfig(configMap)
	})

	return nil
}

func reloadConfig(configMap *v1.ConfigMap) (*v1.ConfigMap, error) {
	if configMap == nil {
		return nil, nil
	}

	if configMap.Name != config.Name ||
		configMap.Namespace != config.Namespace {
		return configMap, nil
	}

	cfg, err := config.ReadConfig(config.Name, configMap)
	if err != nil {
		return configMap, err
	}

	return configMap, config.Set(cfg)
}

func setup(ctx context.Context, apply apply.Apply, cm corecontrollers.ConfigMapClient) error {
	cfg, err := config.Lookup(ctx, config.Namespace, config.Name, cm)
	if err != nil {
		return err
	}

	os := objectset.NewObjectSet()
	os.Add(&v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: config.Namespace,
		},
	})

	if cfg.InitialDataVersion < 1 {
		os.Add(&v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "fleet",
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
				},
			},
		})
		os.Add(&fleet.ClusterGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "default",
				Namespace: "fleet",
			},
		})
	}

	err = apply.
		WithDynamicLookup().
		WithSetID("initial-data").
		WithNoDelete().
		Apply(os)
	if err != nil {
		return err
	}

	cfg.InitialDataVersion = 1
	return config.Store(cfg, config.Namespace, cm)
}
