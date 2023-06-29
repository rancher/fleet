// Package match is used to test matching a bundles to a target on the command line. (fleetapply)
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

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/internal/pkg/bundlereader"
	"github.com/rancher/fleet/internal/pkg/helmdeployer"
	"github.com/rancher/fleet/internal/pkg/manifest"
	"github.com/rancher/fleet/internal/controller/options"
	"github.com/rancher/fleet/internal/controller/target/matcher"

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
		return printMatch(bundle, m, opts.Output)
	}

	return printMatch(bundle, bm.MatchForTarget(opts.Target), opts.Output)
}

func printMatch(bundle *fleet.Bundle, target *fleet.BundleTarget, output io.Writer) error {
	if target == nil {
		return errors.New("no match found")
	}
	fmt.Fprintf(os.Stderr, "# Matched: %s\n", target.Name)
	if output == nil {
		return nil
	}

	opts := options.Merge(bundle.Spec.BundleDeploymentOptions, target.BundleDeploymentOptions)

	manifest, err := manifest.New(bundle.Spec.Resources)
	if err != nil {
		return err
	}

	objs, err := helmdeployer.Template(bundle.Name, manifest, opts)
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
