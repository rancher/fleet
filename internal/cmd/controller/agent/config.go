package agent

import (
	"context"

	"github.com/rancher/fleet/internal/client"
	"github.com/rancher/fleet/internal/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type ConfigOptions struct {
	Labels   map[string]string
	ClientID string
}

func agentConfig(ctx context.Context, agentNamespace, controllerNamespace string, cg *client.Getter, opts *ConfigOptions) ([]runtime.Object, error) {
	if opts == nil {
		opts = &ConfigOptions{}
	}

	client, err := cg.Get()
	if err != nil {
		return nil, err
	}

	// sanity test the controllerNamespace is correct
	_, err = config.Lookup(ctx, controllerNamespace, config.ManagerConfigName, client.Core.ConfigMap())
	if err != nil {
		return nil, err
	}

	return configObjects(agentNamespace, opts.Labels, opts.ClientID)
}

func configObjects(controllerNamespace string, clusterLabels map[string]string, clientID string) ([]runtime.Object, error) {
	cm, err := config.ToConfigMap(controllerNamespace, config.AgentConfigName, &config.Config{
		Labels:   clusterLabels,
		ClientID: clientID,
	})
	if err != nil {
		return nil, err
	}
	cm.Name = "fleet-agent"
	return []runtime.Object{
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: controllerNamespace,
			},
		},
		cm,
	}, nil
}
