package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"
)

type Options struct {
	Compress       bool
	Labels         map[string]string
	ServiceAccount string
	TargetsFile    string
}

func Open(ctx context.Context, name, baseDir, file string, opts *Options) (*Bundle, error) {
	if baseDir == "" {
		baseDir = "."
	}

	if file == "-" {
		return Read(ctx, name, baseDir, os.Stdin, opts)
	}

	var (
		in io.Reader
	)

	if file == "" {
		file = filepath.Join(baseDir, "fleet.yaml")
		if _, err := os.Stat(file); os.IsNotExist(err) {
			file = filepath.Join(baseDir, "bundle.yaml")
		}
		if f, err := os.Open(file); os.IsNotExist(err) {
			in = bytes.NewBufferString("{}")
		} else if err != nil {
			return nil, err
		} else {
			in = f
			defer f.Close()
		}
	} else {
		f, err := os.Open(filepath.Join(baseDir, file))
		if err != nil {
			return nil, err
		}
		defer f.Close()
		in = f
	}

	return Read(ctx, name, baseDir, in, opts)
}

func Read(ctx context.Context, name, baseDir string, bundleSpecReader io.Reader, opts *Options) (*Bundle, error) {
	if opts == nil {
		opts = &Options{}
	}

	data, err := ioutil.ReadAll(bundleSpecReader)
	if err != nil {
		return nil, err
	}

	bundle, err := read(ctx, name, baseDir, bytes.NewBuffer(data), opts)
	if err != nil {
		return nil, err
	}

	if size, err := size(bundle.Definition); err != nil {
		return nil, err
	} else if size < 1000000 {
		return bundle, nil
	}

	newOpts := *opts
	newOpts.Compress = true
	return read(ctx, name, baseDir, bytes.NewBuffer(data), &newOpts)
}

func size(bundle *fleet.Bundle) (int, error) {
	marshalled, err := json.Marshal(bundle)
	if err != nil {
		return 0, err
	}
	return len(marshalled), nil
}

func read(ctx context.Context, name, baseDir string, bundleSpecReader io.Reader, opts *Options) (*Bundle, error) {
	if opts == nil {
		opts = &Options{}
	}

	if baseDir == "" {
		baseDir = "./"
	}

	bytes, err := ioutil.ReadAll(bundleSpecReader)
	if err != nil {
		return nil, err
	}

	bundle := &fleet.BundleSpec{}
	if err := yaml.Unmarshal(bytes, &bundle); err != nil {
		return nil, err
	}

	meta, err := readMetadata(bytes)
	if err != nil {
		return nil, err
	}

	meta.Name = name
	setTargetNames(bundle)

	overlays, err := readOverlays(ctx, meta, bundle, opts.Compress, baseDir)
	if err != nil {
		return nil, err
	}

	resources, err := readResources(ctx, meta, opts.Compress, baseDir)
	if err != nil {
		return nil, err
	}

	bundle.Resources = resources
	assignOverlay(bundle, overlays)

	def := &fleet.Bundle{
		ObjectMeta: meta.ObjectMeta,
		Spec:       *bundle,
	}

	for k, v := range opts.Labels {
		if def.Labels == nil {
			def.Labels = map[string]string{}
		}
		def.Labels[k] = v
	}

	if opts.ServiceAccount != "" {
		def.Spec.ServiceAccount = opts.ServiceAccount
	}

	def, err = appendTargets(def, opts.TargetsFile)
	if err != nil {
		return nil, err
	}

	if len(def.Spec.Targets) == 0 {
		def.Spec.Targets = []fleet.BundleTarget{
			{
				Name:         "default",
				ClusterGroup: "default",
			},
		}
	}

	return New(def)
}

func appendTargets(def *fleet.Bundle, targetsFile string) (*fleet.Bundle, error) {
	if targetsFile == "" {
		return def, nil
	}

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

	return def, nil
}

func assignOverlay(bundle *fleet.BundleSpec, overlays map[string][]fleet.BundleResource) {
	defined := map[string]bool{}
	for i := range bundle.Overlays {
		defined[bundle.Overlays[i].Name] = true
		bundle.Overlays[i].Resources = overlays[bundle.Overlays[i].Name]
	}
	for name, resources := range overlays {
		if defined[name] {
			continue
		}
		bundle.Overlays = append(bundle.Overlays, fleet.BundleOverlay{
			Name:      name,
			Resources: resources,
		})
	}

	sort.Slice(bundle.Overlays, func(i, j int) bool {
		return bundle.Overlays[i].Name < bundle.Overlays[j].Name
	})
}

func setTargetNames(spec *fleet.BundleSpec) {
	for i, target := range spec.Targets {
		if target.Name == "" {
			spec.Targets[i].Name = fmt.Sprintf("target%03d", i)
		}
	}
}

func overlays(bundle *fleet.BundleSpec) []string {
	overlayNames := sets.String{}

	for _, target := range bundle.Targets {
		overlayNames.Insert(target.Overlays...)
	}

	for _, overlay := range bundle.Overlays {
		overlayNames.Insert(overlay.Overlays...)
	}

	return overlayNames.List()
}

type bundleMeta struct {
	metav1.ObjectMeta `json:",inline,omitempty"`
	Manifests         string `json:"manifestsDir,omitempty"`
	Overlays          string `json:"overlaysDir,omitempty"`
	Kustomize         string `json:"kustomizeDir,omitempty"`
	Chart             string `json:"chart,omitempty"`
}

func readMetadata(bytes []byte) (*bundleMeta, error) {
	temp := &bundleMeta{}
	return temp, yaml.Unmarshal(bytes, temp)
}
