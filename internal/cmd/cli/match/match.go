// Package match is used to test matching a bundles to a target on the command line.
//
// It's not used by fleet, but it is available in the fleet CLI as "test" sub
// command. The tests in fleet-examples use it.
package match

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/rancher/fleet/internal/bundlereader"
	"github.com/rancher/fleet/internal/cmd/controller/options"
	"github.com/rancher/fleet/internal/cmd/controller/target/matcher"
	"github.com/rancher/fleet/internal/helmdeployer"
	"github.com/rancher/fleet/internal/manifest"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/yaml"
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
		bundle *fleet.Bundle
		err    error
	)

	if opts.BundleFile == "" {
		bundle, _, err = bundlereader.Open(ctx, "test", opts.BaseDir, opts.BundleSpec, nil)
		if err != nil {
			return err
		}
	} else {
		data, err := os.ReadFile(opts.BundleFile)
		if err != nil {
			return err
		}

		bundle = &fleet.Bundle{}
		if err := yaml.Unmarshal(data, bundle); err != nil {
			return err
		}
	}

	bm, err := matcher.New(bundle)
	if err != nil {
		return err
	}

	if opts.Target == "" {
		m := bm.Match(opts.ClusterName, map[string]map[string]string{
			opts.ClusterGroup: opts.ClusterGroupLabels,
		}, opts.ClusterLabels)
		return printMatch(ctx, bundle, m, opts.Output)
	}

	return printMatch(ctx, bundle, bm.MatchForTarget(opts.Target), opts.Output)
}

func printMatch(ctx context.Context, bundle *fleet.Bundle, target *fleet.BundleTarget, output io.Writer) error {
	if target == nil {
		return errors.New("no match found")
	}
	fmt.Fprintf(os.Stderr, "# Matched: %s\n", target.Name)
	if output == nil {
		return nil
	}

	opts := options.Merge(bundle.Spec.BundleDeploymentOptions, target.BundleDeploymentOptions)

	manifest := manifest.New(bundle.Spec.Resources)

	rel, err := helmdeployer.Template(ctx, bundle.Name, manifest, opts, "")
	if err != nil {
		return err
	}

	objs, err := yaml.ToObjects(bytes.NewBufferString(rel.Manifest))
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
