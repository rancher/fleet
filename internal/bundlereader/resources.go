package bundlereader

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"

	"github.com/rancher/fleet/internal/bundlereader/progress"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"github.com/rancher/wrangler/v3/pkg/data"

	"sigs.k8s.io/yaml"
)

var hasOCIURL = regexp.MustCompile(`^oci:\/\/`)

// readResources reads and downloads all resources from the bundle. Resources
// can be downloaded and are spread across multiple directories.
func readResources(ctx context.Context, spec *fleet.BundleSpec, compress bool, base string, auth Auth, helmRepoURLRegex string) ([]fleet.BundleResource, error) {
	directories, err := addDirectory(base, ".", ".")
	if err != nil {
		return nil, err
	}

	var chartDirs []*fleet.HelmOptions

	if spec.Helm != nil && spec.Helm.Chart != "" {
		if err := parseValuesFiles(base, spec.Helm); err != nil {
			return nil, err
		}
		chartDirs = append(chartDirs, spec.Helm)
	}

	for _, target := range spec.Targets {
		if target.Helm != nil {
			err := parseValuesFiles(base, target.Helm)
			if err != nil {
				return nil, err
			}
			if target.Helm.Chart != "" {
				chartDirs = append(chartDirs, target.Helm)
			}
		}
	}

	directories, err = addRemoteCharts(directories, base, chartDirs, auth, helmRepoURLRegex)
	if err != nil {
		return nil, err
	}

	// helm chart dependency update is enabled by default
	disableDepsUpdate := false
	if spec.Helm != nil {
		disableDepsUpdate = spec.Helm.DisableDependencyUpdate
	}

	loadOpts := loadOpts{
		compress:           compress,
		disableDepsUpdate:  disableDepsUpdate,
		ignoreApplyConfigs: ignoreApplyConfigs(spec.Helm, spec.Targets...),
	}
	resources, err := loadDirectories(ctx, loadOpts, directories...)
	if err != nil {
		return nil, err
	}

	// flatten map to slice
	var result []fleet.BundleResource
	for _, r := range resources {
		result = append(result, r...)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result, nil
}

type loadOpts struct {
	compress           bool
	disableDepsUpdate  bool
	ignoreApplyConfigs []string
}

// ignoreApplyConfigs returns a list of config files that should not be added to the
// bundle's resources. Their contents are converted into deployment options.
// This includes:
// * fleet.yaml
// * spec.Helm.ValuesFiles
// * spec.Targets[].Helm.ValuesFiles
func ignoreApplyConfigs(spec *fleet.HelmOptions, targets ...fleet.BundleTarget) []string {
	ignore := []string{"fleet.yaml"}
	if spec != nil {
		ignore = append(ignore, spec.ValuesFiles...)
	}

	for _, target := range targets {
		if target.Helm == nil {
			continue
		}
		ignore = append(ignore, target.Helm.ValuesFiles...)
	}

	return ignore
}

// directory represents a directory to load resources from. The directory can
// be created from an external Helm chart, or a local path.
// One bundle can consist of multiple directories.
type directory struct {
	// prefix is the generated top level dir of the chart, e.g. '.chart/1234'
	prefix string
	// base is the directory on disk to load the files from
	base string
	// source is the chart URL to download the chart from
	source string
	// version is the version of the chart
	version string
	// auth is the auth to use for the chart URL
	auth Auth
}

func addDirectory(base, customDir, defaultDir string) ([]directory, error) {
	var directories []directory
	if customDir == "" {
		if _, err := os.Stat(filepath.Join(base, defaultDir)); os.IsNotExist(err) {
			return directories, nil
		} else if err != nil {
			return directories, err
		}
		customDir = defaultDir
	}

	return []directory{{
		prefix: defaultDir,
		base:   base,
		source: customDir,
	}}, nil
}

func parseValuesFiles(base string, chart *fleet.HelmOptions) (err error) {
	if len(chart.ValuesFiles) != 0 {
		valuesMap, err := generateValues(base, chart)
		if err != nil {
			return err
		}

		if len(valuesMap.Data) != 0 {
			chart.Values = valuesMap
		}
	}

	return nil
}

func generateValues(base string, chart *fleet.HelmOptions) (valuesMap *fleet.GenericMap, err error) {
	valuesMap = &fleet.GenericMap{}
	if chart.Values != nil {
		valuesMap = chart.Values
	}
	for _, value := range chart.ValuesFiles {
		valuesByte, err := os.ReadFile(base + "/" + value)
		if err != nil {
			return nil, fmt.Errorf("reading values file: %s/%s: %w", base, value, err)
		}
		tmpDataOpt := &fleet.GenericMap{}
		err = yaml.Unmarshal(valuesByte, tmpDataOpt)
		if err != nil {
			return nil, fmt.Errorf("reading values file: %s/%s: %w", base, value, err)
		}
		valuesMap = mergeGenericMap(valuesMap, tmpDataOpt)
	}

	return valuesMap, nil
}

func mergeGenericMap(first, second *fleet.GenericMap) *fleet.GenericMap {
	result := &fleet.GenericMap{Data: make(map[string]interface{})}
	result.Data = data.MergeMaps(first.Data, second.Data)
	return result
}

// addRemoteCharts gets the chart url from a helm repo server and returns a `directory` struct.
// For every chart that is not on disk, create a directory struct that contains the charts URL as path.
func addRemoteCharts(directories []directory, base string, charts []*fleet.HelmOptions, auth Auth, helmRepoURLRegex string) ([]directory, error) {
	for _, chart := range charts {
		if _, err := os.Stat(filepath.Join(base, chart.Chart)); os.IsNotExist(err) || chart.Repo != "" {
			shouldAddAuthToRequest, err := shouldAddAuthToRequest(helmRepoURLRegex, chart.Repo, chart.Chart)
			if err != nil {
				return nil, fmt.Errorf("failed to add auth to request for %s: %w", downloadChartError(*chart), err)
			}
			if !shouldAddAuthToRequest {
				auth = Auth{}
			}

			chartURL, err := chartURL(*chart, auth)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve URL of %s: %w", downloadChartError(*chart), err)
			}

			directories = append(directories, directory{
				prefix:  checksum(chart),
				base:    base,
				source:  chartURL,
				auth:    auth,
				version: chart.Version,
			})
		}
	}
	return directories, nil
}

func downloadChartError(c fleet.HelmOptions) string {
	return fmt.Sprintf(
		"repo=%s chart=%s version=%s",
		c.Repo,
		c.Chart,
		c.Version,
	)
}

func shouldAddAuthToRequest(helmRepoURLRegex, repo, chart string) (bool, error) {
	if helmRepoURLRegex == "" {
		return true, nil
	}
	if repo == "" {
		return regexp.MatchString(helmRepoURLRegex, chart)
	}

	return regexp.MatchString(helmRepoURLRegex, repo)
}

func checksum(helm *fleet.HelmOptions) string {
	if helm == nil {
		return "none"
	}
	return fmt.Sprintf(".chart/%x", sha256.Sum256([]byte(helm.Chart + ":" + helm.Repo + ":" + helm.Version)[:]))
}

// loadDirectories loads all resources from a bundle's directories
func loadDirectories(ctx context.Context, opts loadOpts, directories ...directory) (map[string][]fleet.BundleResource, error) {
	var (
		sem    = semaphore.NewWeighted(4)
		result = map[string][]fleet.BundleResource{}
		l      = sync.Mutex{}
		p      = progress.NewProgress()
	)
	defer p.Close()

	eg, ctx := errgroup.WithContext(ctx)

	for _, dir := range directories {
		if err := sem.Acquire(ctx, 1); err != nil {
			return nil, err
		}
		dir := dir
		eg.Go(func() error {
			defer sem.Release(1)
			resources, err := loadDirectory(ctx, opts, dir)
			if err != nil {
				return fmt.Errorf("loading directory %s, %s: %w", dir.prefix, dir.base, err)
			}

			key := dir.prefix
			if key == "" {
				key = dir.source
			}

			l.Lock()
			result[key] = resources
			l.Unlock()
			return nil
		})
	}

	return result, eg.Wait()
}
