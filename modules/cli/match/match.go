package match

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/rancher/fleet/pkg/render"

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

	bundle, err := bundle.Open(ctx, opts.BaseDir, opts.BundleFile)
	if err != nil {
		return err
	}

	if opts.Target == "" {
		m := bundle.Match(opts.ClusterGroup, opts.ClusterGroupLabels, opts.ClusterLabels)
		return printMatch(m, opts.Output)
	}

	return printMatch(bundle.MatchForTarget(opts.Target), opts.Output)
}

func printMatch(m *bundle.Match, output io.Writer) error {
	if m == nil {
		return nil
	}
	fmt.Fprintf(os.Stderr, "%s\n", m.Target.Name)
	if output == nil {
		return nil
	}

	manifest, err := m.Manifest()
	if err != nil {
		return err
	}

	t, err := render.ToChart(m.Bundle.Definition.Name, manifest)
	if err != nil {
		return err
	}

	_, err = io.Copy(output, t)
	return err
}
