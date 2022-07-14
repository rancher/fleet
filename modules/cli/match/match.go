package match

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/bundle"
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
		b   *bundle.Bundle
		err error
	)

	if opts.BundleFile == "" {
		b, err = bundle.Open(ctx, "test", opts.BaseDir, opts.BundleSpec, nil)
		if err != nil {
			return err
		}
	} else {
		data, err := ioutil.ReadFile(opts.BundleFile)
		if err != nil {
			return err
		}

		bundleConfig := &fleet.Bundle{}
		if err := yaml.Unmarshal(data, bundleConfig); err != nil {
			return err
		}

		b, err = bundle.New(bundleConfig)
		if err != nil {
			return err
		}
	}

	if opts.Target == "" {
		m := b.Match(opts.ClusterName, map[string]map[string]string{
			opts.ClusterGroup: opts.ClusterGroupLabels,
		}, opts.ClusterLabels)
		return printMatch(b, m, opts.Output)
	}

	return printMatch(b, b.MatchForTarget(opts.Target), opts.Output)
}

func printMatch(bundle *bundle.Bundle, m *bundle.Match, output io.Writer) error {
	if m == nil {
		return errors.New("no match found")
	}
	fmt.Fprintf(os.Stderr, "# Matched: %s\n", m.Target.Name)
	if output == nil {
		return nil
	}

	opts := options.Calculate(&bundle.Definition.Spec, m.Target)

	manifest, err := manifest.New(&bundle.Definition.Spec)
	if err != nil {
		return err
	}

	objs, err := helmdeployer.Template(m.Bundle.Definition.Name, manifest, opts)
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
