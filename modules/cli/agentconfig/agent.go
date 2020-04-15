package agentconfig

import (
	"context"
	"io"

	"github.com/rancher/fleet/modules/cli/pkg/client"
	"github.com/rancher/fleet/pkg/basic"
	"github.com/rancher/fleet/pkg/config"
	"github.com/rancher/wrangler/pkg/yaml"
	"k8s.io/apimachinery/pkg/runtime"
)

type Options struct {
	Labels map[string]string
}

func AgentConfig(ctx context.Context, controllerNamespace string, cg *client.Getter, output io.Writer, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	client, err := cg.Get()
	if err != nil {
		return err
	}

	// sanity test the controllerNamespace is correct
	_, err = config.Lookup(ctx, controllerNamespace, config.ManagerConfigName, client.Core.ConfigMap())
	if err != nil {
		return err
	}

	objs, err := Objects(controllerNamespace, opts.Labels)
	if err != nil {
		return err
	}

	data, err := yaml.Export(objs...)
	if err != nil {
		return err
	}

	_, err = output.Write(data)
	return err
}

func Objects(controllerNamespace string, clusterLabels map[string]string) ([]runtime.Object, error) {
	cm, err := config.ToConfigMap(controllerNamespace, config.AgentConfigName, &config.Config{
		Labels: clusterLabels,
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
