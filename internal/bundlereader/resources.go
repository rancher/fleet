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

type Auth struct {
	Username           string `json:"username,omitempty"`
	Password           string `json:"password,omitempty"`
	CABundle           []byte `json:"caBundle,omitempty"`
	SSHPrivateKey      []byte `json:"sshPrivateKey,omitempty"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty"`
}

// readResources reads and downloads all resources from the bundle
func readResources(ctx context.Context, spec *fleet.BundleSpec, compress bool, base string, auth Auth, helmRepoURLRegex string) ([]fleet.BundleResource, error) {
	directories, err := addDirectory(base, ".", ".")
	if err != nil {
		return nil, err
	}

	var chartDirs []*fleet.HelmOptions

	if spec.Helm != nil && spec.Helm.Chart != "" {
		if err := parseValueFiles(base, spec.Helm); err != nil {
			return nil, err
		}
		chartDirs = append(chartDirs, spec.Helm)
	}

	for _, target := range spec.Targets {
		if target.Helm != nil {
			err := parseValueFiles(base, target.Helm)
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
	resources, err := loadDirectories(ctx, compress, disableDepsUpdate, directories...)
	if err != nil {
		return nil, err
	}

	var result []fleet.BundleResource
	for _, resources := range resources {
		result = append(result, resources...)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result, nil
}

type directory struct {
	prefix  string
	base    string
	source  string
	key     string
	version string
	auth    Auth
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
		key:    defaultDir,
	}}, nil
}

func parseValueFiles(base string, chart *fleet.HelmOptions) (err error) {
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
			return nil, err
		}
		tmpDataOpt := &fleet.GenericMap{}
		err = yaml.Unmarshal(valuesByte, tmpDataOpt)
		if err != nil {
			return nil, err
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
				return nil, err
			}
			if !shouldAddAuthToRequest {
				auth = Auth{}
			}

			chartURL, err := ChartURL(*chart, auth)
			if err != nil {
				return nil, err
			}

			directories = append(directories, directory{
				prefix:  checksum(chart),
				base:    base,
				source:  chartURL,
				key:     checksum(chart),
				auth:    auth,
				version: chart.Version,
			})
		}
	}
	return directories, nil
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

func loadDirectories(ctx context.Context, compress bool, disableDepsUpdate bool, directories ...directory) (map[string][]fleet.BundleResource, error) {
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
			resources, err := LoadDirectory(ctx, compress, disableDepsUpdate, dir.prefix, dir.base, dir.source, dir.version, dir.auth)
			if err != nil {
				return err
			}

			key := dir.key
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
