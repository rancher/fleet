package helmupdater

import (
	"os"
	"path/filepath"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/downloader"
	"helm.sh/helm/v4/pkg/getter"
	"helm.sh/helm/v4/pkg/registry"
)

const (
	ChartYaml = "Chart.yaml"
)

// ChartYAMLExists checks if the provided path is a directory containing a `Chart.yaml` file.
// Returns true if it does, false otherwise or if an error happens when checking `<path>/Chart.yaml`.
func ChartYAMLExists(path string) bool {
	chartPath := filepath.Join(path, ChartYaml)
	if _, err := os.Stat(chartPath); err != nil {
		return false
	}
	return true
}

// UpdateHelmDependencies checks if the helm chart located at the given directory has unmet dependencies and, if so, updates them
func UpdateHelmDependencies(path string) error {
	// load the chart to check if there are unmet dependencies first
	chartRequested, err := loader.Load(path)
	if err != nil {
		return err
	}

	if req := chartRequested.Metadata.Dependencies; req != nil {
		// Convert []*v2.Dependency to []chart.Dependency
		deps := make([]chart.Dependency, len(req))
		for i, d := range req {
			deps[i] = d
		}
		if err := action.CheckDependencies(chartRequested, deps); err != nil {
			settings := cli.New()
			registryClient, err := registry.NewClient(
				registry.ClientOptDebug(settings.Debug),
				registry.ClientOptEnableCache(true),
				registry.ClientOptWriter(os.Stderr),
				registry.ClientOptCredentialsFile(settings.RegistryConfig),
			)
			if err != nil {
				return err
			}
			man := &downloader.Manager{
				Out:              os.Stdout,
				ChartPath:        path,
				Keyring:          "",
				SkipUpdate:       false,
				Getters:          getter.All(settings),
				RepositoryConfig: settings.RegistryConfig,
				RepositoryCache:  settings.RepositoryCache,
				ContentCache:     settings.ContentCache,
				Debug:            settings.Debug,
				RegistryClient:   registryClient,
			}
			if err := man.Update(); err != nil {
				return err
			}
		}
	}
	return nil
}
