// Package bundlereader creates a bundle from a source and adds all the
// referenced resources, as well as image scans.
package bundlereader

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

	"github.com/rancher/fleet/internal/fleetyaml"
	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// Options include the GitRepo overrides, which are passed via command line args
type Options struct {
	BundleFile       string
	Compress         bool
	Labels           map[string]string
	ServiceAccount   string
	TargetsFile      string
	TargetNamespace  string
	Paused           bool
	SyncGeneration   int64
	Auth             Auth
	HelmRepoURLRegex string
	KeepResources    bool
	DeleteNamespace  bool
	CorrectDrift     *fleet.CorrectDrift
}

// NewBundle reads the fleet.yaml, from stdin, or basedir, or a file in basedir.
// Then it reads/downloads all referenced resources. It returns the populated
// bundle and any existing imagescans.
func NewBundle(ctx context.Context, name, baseDir, file string, opts *Options) (*fleet.Bundle, []*fleet.ImageScan, error) {
	if baseDir == "" {
		baseDir = "."
	}

	if file == "-" {
		b, s, err := loadBundle(ctx, name, baseDir, os.Stdin, opts)
		if err != nil {
			return b, s, fmt.Errorf("failed to process bundle from STDIN: %w", err)
		}
	}

	var (
		in io.Reader
	)

	if file == "" {
		if file, err := setupIOReader(baseDir); err != nil {
			return nil, nil, fmt.Errorf("failed to open existing fleet.yaml in %q: %w", baseDir, err)
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
			return nil, nil, fmt.Errorf("failed to open file %q: %w", file, err)
		}
		defer f.Close()
		in = f
	}

	b, s, err := loadBundle(ctx, name, baseDir, in, opts)
	if err != nil {
		return b, s, fmt.Errorf("failed to process bundle: %w", err)
	}

	return b, s, nil
}

// Try accessing the documented, primary fleet.yaml extension first. If that returns an "IsNotExist" error, then we
// try the fallback extension. If we receive "IsNotExist" errors for both file extensions, then we return a "nil" file
// and a "nil" error. If either return a non-"IsNotExist" error, then we return the error immediately.
func setupIOReader(baseDir string) (*os.File, error) {
	if file, err := os.Open(fleetyaml.GetFleetYamlPath(baseDir, false)); err != nil && !os.IsNotExist(err) {
		return nil, err
	} else if err == nil {
		// File must be closed in the parent function.
		return file, nil
	}

	if file, err := os.Open(fleetyaml.GetFleetYamlPath(baseDir, true)); err != nil && !os.IsNotExist(err) {
		return nil, err
	} else if err == nil {
		// File must be closed in the parent function.
		return file, nil
	}

	return nil, nil
}

// loadBundle creates a bundle and imagescan from a base directory name and a reader (which may represent data from a
// directory structure or from standard input).
func loadBundle(ctx context.Context, name, baseDir string, bundleSpecReader io.Reader, opts *Options) (*fleet.Bundle, []*fleet.ImageScan, error) {
	if opts == nil {
		opts = &Options{}
	}

	data, err := io.ReadAll(bundleSpecReader)
	if err != nil {
		return nil, nil, err
	}

	bundle, scans, err := bundleFromDir(ctx, name, baseDir, data, opts)
	if err != nil {
		return nil, nil, err
	}

	if size, err := size(bundle); err != nil {
		return nil, nil, err
	} else if size < 1000000 {
		return bundle, scans, nil
	}

	newOpts := *opts
	newOpts.Compress = true
	return bundleFromDir(ctx, name, baseDir, data, &newOpts)
}

func size(bundle *fleet.Bundle) (int, error) {
	marshalled, err := json.Marshal(bundle)
	if err != nil {
		return 0, err
	}
	return len(marshalled), nil
}

// bundleFromDir reads the fleet.yaml from the bundleData and loads all resources
func bundleFromDir(ctx context.Context, name, baseDir string, bundleData []byte, opts *Options) (*fleet.Bundle, []*fleet.ImageScan, error) {
	if opts == nil {
		opts = &Options{}
	}

	if baseDir == "" {
		baseDir = "./"
	}

	fy := &fleet.FleetYAML{}
	if err := yaml.Unmarshal(bundleData, fy); err != nil {
		return nil, nil, fmt.Errorf("reading fleet.yaml: %w", err)
	}

	// Validate fleet.yaml semantic content before creating the Bundle
	if err := validateFleetYAML(fy); err != nil {
		return nil, nil, fmt.Errorf("validating fleet.yaml: %w", err)
	}

	var scans []*fleet.ImageScan
	for i, scan := range fy.ImageScans {
		if scan.Image == "" {
			continue
		}
		if scan.TagName == "" {
			return nil, nil, errors.New("the name of scan is required")
		}

		scans = append(scans, &fleet.ImageScan{
			ObjectMeta: metav1.ObjectMeta{
				Name: names.SafeConcatName("imagescan", name, strconv.Itoa(i)),
			},
			Spec: scan.ImageScanSpec,
		})
	}

	fy.Targets = append(fy.Targets, fy.TargetCustomizations...)

	meta, err := readMetadata(bundleData)
	if err != nil {
		return nil, nil, err
	}

	meta.Name = name
	if fy.Name != "" {
		meta.Name = fy.Name
	}

	setTargetNames(&fy.BundleSpec)

	propagateHelmChartProperties(&fy.BundleSpec)

	resources, err := readResources(ctx, &fy.BundleSpec, opts.Compress, baseDir, opts.Auth, opts.HelmRepoURLRegex, opts.BundleFile)
	if err != nil {
		return nil, nil, fmt.Errorf("failed reading resources for %q: %w", baseDir, err)
	}

	fy.Resources = resources

	bundle := &fleet.Bundle{
		ObjectMeta: meta.ObjectMeta,
		Spec:       fy.BundleSpec,
	}

	for k, v := range opts.Labels {
		if bundle.Labels == nil {
			bundle.Labels = make(map[string]string)
		}
		bundle.Labels[k] = v
	}

	// apply additional labels from spec
	for k, v := range fy.Labels {
		if bundle.Labels == nil {
			bundle.Labels = make(map[string]string)
		}
		bundle.Labels[k] = v
	}

	if opts.ServiceAccount != "" {
		bundle.Spec.ServiceAccount = opts.ServiceAccount
	}

	bundle.Spec.ForceSyncGeneration = opts.SyncGeneration

	// Targets defined in the GitRepo are stored in the targets file, which will be used if OverrideTargets is not provided.
	// Use targets from OverrideTargets if found in the fleet.yaml.
	if fy.OverrideTargets != nil {
		logrus.Debugf("Overriding targets for Bundle '%s' ", bundle.Name)
		for _, target := range fy.OverrideTargets {
			bundle.Spec.Targets = append(bundle.Spec.Targets, fleet.BundleTarget{
				Name:                 target.Name,
				ClusterName:          target.ClusterName,
				ClusterSelector:      target.ClusterSelector,
				ClusterGroup:         target.ClusterGroup,
				ClusterGroupSelector: target.ClusterGroupSelector,
			})
			bundle.Spec.TargetRestrictions = append(bundle.Spec.TargetRestrictions, fleet.BundleTargetRestriction(target))
		}
	} else {
		bundle, err = appendTargets(bundle, opts.TargetsFile)
		if err != nil {
			return nil, nil, err
		}
	}

	if len(bundle.Spec.Targets) == 0 {
		bundle.Spec.Targets = []fleet.BundleTarget{
			{
				Name:         "default",
				ClusterGroup: "default",
			},
		}
	}

	if opts.TargetNamespace != "" {
		bundle.Spec.TargetNamespace = opts.TargetNamespace
		for i := range bundle.Spec.Targets {
			bundle.Spec.Targets[i].TargetNamespace = opts.TargetNamespace
		}
	}

	if opts.Paused {
		bundle.Spec.Paused = true
	}

	if opts.KeepResources {
		bundle.Spec.KeepResources = opts.KeepResources
	}

	if opts.DeleteNamespace {
		bundle.Spec.DeleteNamespace = opts.DeleteNamespace
	}

	if opts.CorrectDrift != nil && opts.CorrectDrift.Enabled {
		bundle.Spec.CorrectDrift = opts.CorrectDrift
	}

	return bundle, scans, nil
}

// propagateHelmChartProperties propagates root Helm chart properties to the child targets.
// This is necessary, so we can download the correct chart version for each target.
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
