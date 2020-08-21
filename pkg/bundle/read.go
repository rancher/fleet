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
	Compress bool
}

func Open(ctx context.Context, baseDir, file string, opts *Options) (*Bundle, error) {
	if baseDir == "" {
		baseDir = "."
	}

	if file == "-" {
		return Read(ctx, baseDir, os.Stdin, opts)
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

	return Read(ctx, baseDir, in, opts)
}

func Read(ctx context.Context, baseDir string, bundleSpecReader io.Reader, opts *Options) (*Bundle, error) {
	if opts == nil {
		opts = &Options{}
	}

	data, err := ioutil.ReadAll(bundleSpecReader)
	if err != nil {
		return nil, err
	}

	bundle, err := read(ctx, opts.Compress, baseDir, bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}

	if size, err := size(bundle.Definition); err != nil {
		return nil, err
	} else if size < 1000000 {
		return bundle, nil
	}

	return read(ctx, true, baseDir, bytes.NewBuffer(data))
}

func size(bundle *fleet.Bundle) (int, error) {
	marshalled, err := json.Marshal(bundle)
	if err != nil {
		return 0, err
	}
	return len(marshalled), nil
}

func read(ctx context.Context, compress bool, baseDir string, bundleSpecReader io.Reader) (*Bundle, error) {
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

	setTargetNames(bundle)

	overlays, err := readOverlays(ctx, meta, bundle, compress, baseDir)
	if err != nil {
		return nil, err
	}

	resources, err := readResources(ctx, meta, compress, baseDir)
	if err != nil {
		return nil, err
	}

	bundle.Resources = resources
	assignOverlay(bundle, overlays)

	return New(&fleet.Bundle{
		ObjectMeta: meta.ObjectMeta,
		Spec:       *bundle,
	})
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
