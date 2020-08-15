package simulator

import (
	"context"
	"io"

	"github.com/rancher/fleet/modules/agent/pkg/simulator"
	"github.com/rancher/fleet/modules/cli/agentconfig"

	"github.com/rancher/fleet/modules/cli/agentmanifest"

	"github.com/rancher/fleet/modules/cli/pkg/client"
	"github.com/rancher/wrangler/pkg/yaml"
)

type Options struct {
	CA     []byte
	Host   string
	NoCA   bool
	Labels map[string]string
}

func Simulator(ctx context.Context, image, controllerNamespace, tokenName string, simulators int, cg *client.Getter, output io.Writer, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	agentOpts := &agentmanifest.Options{
		CA:   opts.CA,
		Host: opts.Host,
		NoCA: opts.NoCA,
	}

	client, err := cg.Get()
	if err != nil {
		return err
	}

	objs, err := agentmanifest.AgentToken(ctx, controllerNamespace, cg.Kubeconfig, client, tokenName, agentOpts)
	if err != nil {
		return err
	}

	configObjs, err := agentconfig.Objects(controllerNamespace, opts.Labels, "")
	if err != nil {
		return err
	}

	objs, err = simulator.Manifest("", controllerNamespace, image, simulators, append(objs, configObjs...))
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
