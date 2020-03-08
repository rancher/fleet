package bundle

import (
	"context"
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

func Open(ctx context.Context, baseDir, file string) (*Bundle, error) {
	if file == "" {
		file = filepath.Join(baseDir, "bundle.yaml")
	} else if file == "-" {
		return Read(ctx, baseDir, os.Stdin)
	} else {
		file = filepath.Join(baseDir, file)
	}

	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return Read(ctx, baseDir, f)
}

func Read(ctx context.Context, baseDir string, bundleSpecReader io.Reader) (*Bundle, error) {
	if baseDir == "" {
		baseDir = "./"
	}

	bytes, err := ioutil.ReadAll(bundleSpecReader)
	if err != nil {
		return nil, err
	}

	app := &fleet.BundleSpec{}
	if err := yaml.Unmarshal(bytes, &app); err != nil {
		return nil, err
	}

	meta, err := readMetadata(bytes)
	if err != nil {
		return nil, err
	}

	if meta.Name == "" {
		return nil, fmt.Errorf("name is required in the bundle.yaml")
	}

	setTargetNames(app)

	overlays, err := readOverlays(ctx, meta.Name, baseDir, overlays(app)...)
	if err != nil {
		return nil, err
	}

	resources, err := readBaseResources(ctx, meta.Name, baseDir, meta.Manifests)
	if err != nil {
		return nil, err
	}

	translate(app, overlays)
	app.Overlays = sortOverlays(overlays)
	app.Resources = resources

	return New(&fleet.Bundle{
		ObjectMeta: meta.ObjectMeta,
		Spec:       *app,
	})
}

func setTargetNames(spec *fleet.BundleSpec) {
	for i, target := range spec.Targets {
		if target.Name == "" {
			spec.Targets[i].Name = fmt.Sprintf("target%03d", i)
		}
	}
}

func sortOverlays(bundleMap map[string]*fleet.BundleOverlay) (result []fleet.BundleOverlay) {
	var keys []string
	for k := range bundleMap {
		keys = append(keys, k)
	}

	sort.Strings(keys)
	for _, k := range keys {
		result = append(result, *bundleMap[k])
	}

	return
}

func overlays(app *fleet.BundleSpec) []string {
	overlayNames := sets.String{}

	for _, target := range app.Targets {
		overlayNames.Insert(target.Overlays...)
	}

	return overlayNames.List()
}

func translate(bundle *fleet.BundleSpec, bundleMap map[string]*fleet.BundleOverlay) {
	for i := range bundle.Targets {
		for j := range bundle.Targets[i].Overlays {
			bundle.Targets[i].Overlays[j] = bundleMap[bundle.Targets[i].Overlays[j]].Name
		}
	}
}

type bundleMeta struct {
	metav1.ObjectMeta `json:",inline,omitempty"`
	Manifests         string `json:"manifests,omitempty"`
}

func readMetadata(bytes []byte) (*bundleMeta, error) {
	temp := &bundleMeta{}
	return temp, yaml.Unmarshal(bytes, temp)
}
