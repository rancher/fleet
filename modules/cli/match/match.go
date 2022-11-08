// Package match is used to test matching a bundles to a target on the command line. (fleetapply)
package match

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/bundlematcher"
	"github.com/rancher/fleet/pkg/bundlereader"
	"github.com/rancher/fleet/pkg/helmdeployer"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/rancher/fleet/pkg/options"

	"github.com/rancher/wrangler/pkg/yaml"
)

type Options struct {
	Output             io.Writer
	BaseDir            string
	BundleSpec         string
	BundleFile         string
	ClusterName        string
	ClusterGroup       string
	ClusterLabels      map[string]string
	ClusterGroupLabels map[string]string
	Target             string
}

func Match(ctx context.Context, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	var (
		b   *bundlereader.Results
		err error
	)

	if opts.BundleFile == "" {
		b, err = bundlereader.Open(ctx, "test", opts.BaseDir, opts.BundleSpec, nil)
		if err != nil {
			return err
		}
	} else {
		data, err := os.ReadFile(opts.BundleFile)
		if err != nil {
			return err
		}

		bundleConfig := &fleet.Bundle{}
		if err := yaml.Unmarshal(data, bundleConfig); err != nil {
			return err
		}

		b, err = bundlereader.NewResults(bundleConfig)
		if err != nil {
			return err
		}
	}

	bm, err := bundlematcher.New(b.Bundle)
	if err != nil {
		return err
	}

	if opts.Target == "" {
		m := bm.Match(opts.ClusterName, map[string]map[string]string{
			opts.ClusterGroup: opts.ClusterGroupLabels,
		}, opts.ClusterLabels)
		return printMatch(b, m, opts.Output)
	}

	return printMatch(b, bm.MatchForTarget(opts.Target), opts.Output)
}

func printMatch(bundle *bundlereader.Results, target *fleet.BundleTarget, output io.Writer) error {
	if target == nil {
		return errors.New("no match found")
	}
	fmt.Fprintf(os.Stderr, "# Matched: %s\n", target.Name)
	if output == nil {
		return nil
	}

	opts := options.Calculate(&bundle.Bundle.Spec, target)

	manifest, err := manifest.New(&bundle.Bundle.Spec)
	if err != nil {
		return err
	}

	objs, err := helmdeployer.Template(bundle.Bundle.Name, manifest, opts)
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
