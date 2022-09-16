package agent

import (
	"context"

	"github.com/rancher/fleet/modules/cli/pkg/client"
	"github.com/rancher/fleet/pkg/basic"
	"github.com/rancher/fleet/pkg/config"

	"k8s.io/apimachinery/pkg/runtime"
)

type configOptions struct {
	Labels   map[string]string
	ClientID string
}

func agentConfig(ctx context.Context, agentNamespace, controllerNamespace string, cg *client.Getter, opts *configOptions) ([]runtime.Object, error) {
	if opts == nil {
		opts = &configOptions{}
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
		basic.Namespace(controllerNamespace),
		cm,
	}, nil
}
