package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/bundleyaml"

	name1 "github.com/rancher/wrangler/pkg/name"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

type Options struct {
	Compress        bool
	Labels          map[string]string
	ServiceAccount  string
	TargetsFile     string
	TargetNamespace string
	Paused          bool
	SyncGeneration  int64
	Auth            Auth
}

// Open reads the content, from stdin, or basedir, or a file in basedir. It
// returns a bundle with the given name
func Open(ctx context.Context, name, baseDir, file string, opts *Options) (*Bundle, error) {
	if baseDir == "" {
		baseDir = "."
	}

	if file == "-" {
		return mayCompress(ctx, name, baseDir, os.Stdin, opts)
	}

	var (
		in io.Reader
	)

	if file == "" {
		if file, err := setupIOReader(baseDir); err != nil {
			return nil, err
		} else if file != nil {
			in = file
			defer file.Close()
		} else {
			// Create a new buffer if opening both files resulted in "IsNotExist" errors.
			in = bytes.NewBufferString("{}")
		}
	} else {
		f, err := os.Open(filepath.Join(baseDir, file))
		if err != nil {
			return nil, err
		}
		defer f.Close()
		in = f
	}

	return mayCompress(ctx, name, baseDir, in, opts)
}

// Try accessing the documented, primary fleet.yaml extension first. If that returns an "IsNotExist" error, then we
// try the fallback extension. If we receive "IsNotExist" errors for both file extensions, then we return a "nil" file
// and a "nil" error. If either return a non-"IsNotExist" error, then we return the error immediately.
func setupIOReader(baseDir string) (*os.File, error) {
	if file, err := os.Open(bundleyaml.GetFleetYamlPath(baseDir, false)); err != nil && !os.IsNotExist(err) {
		return nil, err
	} else if err == nil {
		// File must be closed in the parent function.
		return file, nil
	}

	if file, err := os.Open(bundleyaml.GetFleetYamlPath(baseDir, true)); err != nil && !os.IsNotExist(err) {
		return nil, err
	} else if err == nil {
		// File must be closed in the parent function.
		return file, nil
	}

	return nil, nil
}

func mayCompress(ctx context.Context, name, baseDir string, bundleSpecReader io.Reader, opts *Options) (*Bundle, error) {
	if opts == nil {
		opts = &Options{}
	}

	data, err := io.ReadAll(bundleSpecReader)
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

type localSpec struct {
	Name   string            `json:"name,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
	fleet.BundleSpec
	TargetCustomizations []fleet.BundleTarget `json:"targetCustomizations,omitempty"`
	ImageScans           []imageScan          `json:"imageScans,omitempty"`
}

type imageScan struct {
	Name string `json:"name,omitempty"`
	fleet.ImageScanSpec
}

func read(ctx context.Context, name, baseDir string, bundleSpecReader io.Reader, opts *Options) (*Bundle, error) {
	if opts == nil {
		opts = &Options{}
	}

	if baseDir == "" {
		baseDir = "./"
	}

	bytes, err := io.ReadAll(bundleSpecReader)
	if err != nil {
		return nil, err
	}

	bundle := &localSpec{}
	if err := yaml.Unmarshal(bytes, bundle); err != nil {
		return nil, err
	}

	var scans []*fleet.ImageScan
	for i, scan := range bundle.ImageScans {
		if scan.Image == "" {
			continue
		}
		if scan.TagName == "" {
			return nil, errors.New("the name of scan is required")
		}

		scans = append(scans, &fleet.ImageScan{
			ObjectMeta: metav1.ObjectMeta{
				Name: name1.SafeConcatName("imagescan", name, strconv.Itoa(i)),
			},
			Spec: scan.ImageScanSpec,
		})
	}

	bundle.BundleSpec.Targets = append(bundle.BundleSpec.Targets, bundle.TargetCustomizations...)

	meta, err := readMetadata(bytes)
	if err != nil {
		return nil, err
	}

	meta.Name = name
	if bundle.Name != "" {
		meta.Name = bundle.Name
	}

	setTargetNames(&bundle.BundleSpec)

	propagateHelmChartProperties(&bundle.BundleSpec)

	resources, err := readResources(ctx, &bundle.BundleSpec, opts.Compress, baseDir, opts.Auth)
	if err != nil {
		return nil, err
	}

	bundle.Resources = resources

	def := &fleet.Bundle{
		ObjectMeta: meta.ObjectMeta,
		Spec:       bundle.BundleSpec,
	}

	for k, v := range opts.Labels {
		if def.Labels == nil {
			def.Labels = make(map[string]string)
		}
		def.Labels[k] = v
	}

	// apply additional labels from spec
	for k, v := range bundle.Labels {
		if def.Labels == nil {
			def.Labels = make(map[string]string)
		}
		def.Labels[k] = v
	}

	if opts.ServiceAccount != "" {
		def.Spec.ServiceAccount = opts.ServiceAccount
	}

	def.Spec.ForceSyncGeneration = opts.SyncGeneration

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

	if opts.TargetNamespace != "" {
		def.Spec.TargetNamespace = opts.TargetNamespace
		for i := range def.Spec.Targets {
			def.Spec.Targets[i].TargetNamespace = opts.TargetNamespace
		}
	}

	if opts.Paused {
		def.Spec.Paused = true
	}

	return New(def, scans...)
}

// propagateHelmChartProperties propagates root Helm chart properties to the child targets.
func propagateHelmChartProperties(spec *fleet.BundleSpec) {
	// Check if there is anything to propagate
	if spec.Helm == nil {
		return
	}
	for _, target := range spec.Targets {
		if target.Helm == nil {
			// This target has nothing to propagate to
			continue
		}
		if target.Helm.Repo == "" {
			target.Helm.Repo = spec.Helm.Repo
		}
		if target.Helm.Chart == "" {
			target.Helm.Chart = spec.Helm.Chart
		}
		if target.Helm.Version == "" {
			target.Helm.Version = spec.Helm.Version
		}
	}
}

func appendTargets(def *fleet.Bundle, targetsFile string) (*fleet.Bundle, error) {
	if targetsFile == "" {
		return def, nil
	}

	data, err := os.ReadFile(targetsFile)
	if err != nil {
		return nil, err
	}

	spec := &fleet.BundleSpec{}
	if err := yaml.Unmarshal(data, spec); err != nil {
		return nil, err
	}

	def.Spec.Targets = append(def.Spec.Targets, spec.Targets...)
	def.Spec.TargetRestrictions = append(def.Spec.TargetRestrictions, spec.TargetRestrictions...)

	return def, nil
}

func setTargetNames(spec *fleet.BundleSpec) {
	for i, target := range spec.Targets {
		if target.Name == "" {
			spec.Targets[i].Name = fmt.Sprintf("target%03d", i)
		}
	}
}

type bundleMeta struct {
	metav1.ObjectMeta `json:",inline,omitempty"`
}

func readMetadata(bytes []byte) (*bundleMeta, error) {
	temp := &bundleMeta{}
	return temp, yaml.Unmarshal(bytes, temp)
}
