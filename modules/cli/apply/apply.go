package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"path/filepath"
	"regexp"

	"github.com/rancher/fleet/modules/cli/pkg/client"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/bundle"
	"github.com/rancher/wrangler/pkg/yaml"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	disallowedChars = regexp.MustCompile("[^a-zA-Z0-9]+")
	multiDash       = regexp.MustCompile("-+")
	ErrNoResources  = errors.New("no resources found to deploy")
)

type Options struct {
	BundleFile     string
	TargetsFile    string
	Compress       bool
	BundleReader   io.Reader
	Output         io.Writer
	ServiceAccount string
	Labels         map[string]string
}

func Apply(ctx context.Context, client *client.Getter, name string, baseDirs []string, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	if len(baseDirs) == 0 {
		baseDirs = []string{"."}
	}

	foundBundle := false
	for i, baseDir := range baseDirs {
		matches, err := filepath.Glob(baseDir)
		if err != nil {
			return fmt.Errorf("invalid path glob %s: %w", baseDir, err)
		}
		for _, baseDir := range matches {
			if i > 0 && opts.Output != nil {
				if _, err := opts.Output.Write([]byte("\n---\n")); err != nil {
					return err
				}
			}
			if err := Dir(ctx, client, name, baseDir, opts); err == ErrNoResources {
				logrus.Warnf("%s: %v", baseDir, err)
				continue
			} else if err != nil {
				return err
			}
			foundBundle = true
		}
	}

	if !foundBundle {
		return fmt.Errorf("no fleet.yaml or bundle.yaml found at the following paths: %v", baseDirs)
	}

	return nil
}

func readBundle(ctx context.Context, baseDir string, opts *Options) (*bundle.Bundle, error) {
	if opts.BundleReader != nil {
		var bundleResource fleet.Bundle
		if err := json.NewDecoder(opts.BundleReader).Decode(&bundleResource); err != nil {
			return nil, err
		}
		return bundle.New(&bundleResource)
	}

	b, err := bundle.Open(ctx, baseDir, opts.BundleFile, &bundle.Options{
		Compress: opts.Compress,
	})
	if err != nil {
		return nil, err
	}

	return appendTargets(b, opts.TargetsFile)
}

func appendTargets(b *bundle.Bundle, targetsFile string) (*bundle.Bundle, error) {
	if targetsFile == "" {
		return b, nil
	}

	def := b.Definition.DeepCopy()
	data, err := ioutil.ReadFile(targetsFile)
	if err != nil {
		return nil, err
	}

	spec := &fleet.BundleSpec{}
	if err := yaml.Unmarshal(data, spec); err != nil {
		return nil, err
	}

	for _, target := range spec.Targets {
		def.Spec.Targets = append(def.Spec.Targets, target)
	}
	for _, targetRestriction := range spec.TargetRestrictions {
		def.Spec.TargetRestrictions = append(def.Spec.TargetRestrictions, targetRestriction)
	}

	return bundle.New(def)
}

func createName(name, baseDir string) string {
	path := filepath.Join(name, baseDir)
	path = disallowedChars.ReplaceAllString(path, "-")
	return multiDash.ReplaceAllString(path, "-")
}

func Dir(ctx context.Context, client *client.Getter, name, baseDir string, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}

	bundle, err := readBundle(ctx, baseDir, opts)
	if err != nil {
		return err
	}

	def := bundle.Definition.DeepCopy()
	def.Namespace = client.Namespace
	def.Name = createName(name, baseDir)
	for k, v := range opts.Labels {
		if def.Labels == nil {
			def.Labels = map[string]string{}
		}
		def.Labels[k] = v
	}

	if opts.ServiceAccount != "" {
		def.Spec.ServiceAccount = opts.ServiceAccount
	}

	if len(def.Spec.Targets) == 0 {
		def.Spec.Targets = []fleet.BundleTarget{
			{
				Name:         "default",
				ClusterGroup: "default",
			},
		}
	}

	if len(def.Spec.Resources) == 0 {
		return ErrNoResources
	}

	b, err := yaml.Export(def)
	if err != nil {
		return err
	}

	if opts.Output == nil {
		err = save(client, def)
	} else {
		_, err = opts.Output.Write(b)
	}

	return err
}

func save(client *client.Getter, bundle *fleet.Bundle) error {
	c, err := client.Get()
	if err != nil {
		return err
	}

	obj, err := c.Fleet.Bundle().Get(bundle.Namespace, bundle.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = c.Fleet.Bundle().Create(bundle)
		if err == nil {
			fmt.Printf("%s/%s\n", bundle.Namespace, bundle.Name)
		}
		return err
	} else if err != nil {
		return err
	}

	obj.Spec = bundle.Spec
	obj.Annotations = mergeMap(obj.Annotations, bundle.Annotations)
	obj.Labels = mergeMap(obj.Labels, bundle.Labels)
	_, err = c.Fleet.Bundle().Update(obj)
	if err == nil {
		fmt.Printf("%s/%s\n", obj.Namespace, obj.Name)
	}
	return err
}

func mergeMap(a, b map[string]string) map[string]string {
	result := map[string]string{}
	for k, v := range a {
		result[k] = v
	}
	for k, v := range b {
		result[k] = v
	}
	return result
}
