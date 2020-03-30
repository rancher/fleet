package agentconfig

import (
	"io"

	"github.com/rancher/fleet/modules/cli/pkg/client"

	"github.com/rancher/fleet/pkg/config"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/rancher/wrangler/pkg/yaml"
)

type Options struct {
	Labels map[string]string
}

func AgentConfig(output io.Writer, cg *client.Getter, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	objs, err := configMap(cg.Namespace, opts.Labels)
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

func configMap(namespace string, clusterLabels map[string]string) ([]runtime.Object, error) {
	cm, err := config.ToConfigMap(namespace, config.AgentConfigName, &config.Config{
		Labels: clusterLabels,
	})
	if err != nil {
		return nil, err
	}
	cm.Name = "fleet-agent"
	return []runtime.Object{
		cm,
	}, nil
}
