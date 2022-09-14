// Package agentconfig builds the configuration for the fleet-agent (fleetcontroller)
package agentconfig

import (
	"context"

	"github.com/rancher/fleet/modules/cli/pkg/client"
	"github.com/rancher/fleet/pkg/basic"
	"github.com/rancher/fleet/pkg/config"
	"k8s.io/apimachinery/pkg/runtime"
)

type Options struct {
	Labels   map[string]string
	ClientID string
}

func AgentConfig(ctx context.Context, agentNamespace, controllerNamespace string, cg *client.Getter, opts *Options) ([]runtime.Object, error) {
	if opts == nil {
		opts = &Options{}
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

	return Objects(agentNamespace, opts.Labels, opts.ClientID)
}

func Objects(controllerNamespace string, clusterLabels map[string]string, clientID string) ([]runtime.Object, error) {
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
