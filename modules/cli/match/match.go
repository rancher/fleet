package match

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/rancher/fleet/pkg/helmdeployer"
	"github.com/rancher/wrangler/pkg/yaml"

	"github.com/rancher/fleet/pkg/bundle"
)

type Options struct {
	Output             io.Writer
	BaseDir            string
	BundleFile         string
	ClusterGroup       string
	ClusterLabels      map[string]string
	ClusterGroupLabels map[string]string
	Target             string
}

func Match(ctx context.Context, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	bundle, err := bundle.Open(ctx, opts.BaseDir, opts.BundleFile, nil)
	if err != nil {
		return err
	}

	if opts.Target == "" {
		m := bundle.Match(map[string]map[string]string{
			opts.ClusterGroup: opts.ClusterGroupLabels,
		}, opts.ClusterLabels)
		return printMatch(m, opts.Output)
	}

	return printMatch(bundle.MatchForTarget(opts.Target), opts.Output)
}

func printMatch(m *bundle.Match, output io.Writer) error {
	if m == nil {
		return nil
	}
	fmt.Fprintf(os.Stderr, "# Matched: %s\n", m.Target.Name)
	if output == nil {
		return nil
	}

	manifest, err := m.Manifest()
	if err != nil {
		return err
	}

	objs, err := helmdeployer.Template(m.Bundle.Definition.Name, manifest, m.Target.BundleDeploymentOptions)
	if err != nil {
		return err
	}

	data, err := yaml.Export(objs...)
	if err != nil {
		return err
	}

	_, err = io.Copy(output, bytes.NewBuffer(data))
	return err
}
