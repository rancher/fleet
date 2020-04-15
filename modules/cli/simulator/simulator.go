package simulator

import (
	"context"
	"io"
	"time"

	"github.com/rancher/fleet/modules/agent/pkg/simulator"
	"github.com/rancher/fleet/modules/cli/agentconfig"

	"github.com/rancher/fleet/modules/cli/agentmanifest"

	"github.com/rancher/fleet/modules/cli/pkg/client"
	"github.com/rancher/wrangler/pkg/yaml"
)

type Options struct {
	TTL    time.Duration
	CA     []byte
	Host   string
	NoCA   bool
	Labels map[string]string
}

func Simulator(ctx context.Context, image, controllerNamespace, clusterGroupName string, simulators int, cg *client.Getter, output io.Writer, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	agentOpts := &agentmanifest.Options{
		TTL:  opts.TTL,
		CA:   opts.CA,
		Host: opts.Host,
		NoCA: opts.NoCA,
	}

	client, err := cg.Get()
	if err != nil {
		return err
	}

	objs, err := agentmanifest.AgentToken(ctx, controllerNamespace, clusterGroupName, cg.Kubeconfig, client, agentOpts)
	if err != nil {
		return err
	}

	configObjs, err := agentconfig.Objects(controllerNamespace, opts.Labels)
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
