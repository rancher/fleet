package bundlereader

import (
	"path/filepath"
	"strings"

	"github.com/rancher/fleet/internal/manifest"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

const (
	chartYAML = "Chart.yaml"
)

func joinAndClean(path, file string) string {
	return strings.TrimPrefix(filepath.Join(path, file), "/")
}

func chartPath(options fleet.BundleDeploymentOptions) (string, string) {
	if options.Helm == nil {
		return chartYAML, ""
	}

	path := options.Helm.Chart
	if len(path) == 0 {
		path = options.Helm.Repo
	}

	return joinAndClean(path, chartYAML), checksum(options.Helm) + "/"
}

func kustomizePath(options fleet.BundleDeploymentOptions) string {
	if options.Kustomize == nil || options.Kustomize.Dir == "" {
		return "kustomization.yaml"
	}
	return joinAndClean(options.Kustomize.Dir, "kustomization.yaml")
}

type Style struct {
	ChartPath     string
	KustomizePath string
	HasChartYAML  bool
	Options       fleet.BundleDeploymentOptions
}

func (s Style) IsHelm() bool {
	return s.HasChartYAML
}

func (s Style) IsKustomize() bool {
	return s.KustomizePath != ""
}

func (s Style) IsRawYAML() bool {
	return !s.IsHelm() && !s.IsKustomize()
}

func matchesExternalChartYAML(externalChartPath string, path string) bool {
	if externalChartPath == "" {
		return false
	}
	if !strings.HasPrefix(path, externalChartPath) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, externalChartPath), "/")
	return (len(parts) == 1 && parts[0] == chartYAML) ||
		(len(parts) == 2 && parts[1] == chartYAML)
}

func DetermineStyle(m *manifest.Manifest, options fleet.BundleDeploymentOptions) Style {
	var (
		chartPath, externalChartPath = chartPath(options)
		kustomizePath                = kustomizePath(options)
		result                       = Style{
			Options: options,
		}
	)

	for _, resource := range m.Resources {
		switch {
		case resource.Name == "":
			// ignore
		case resource.Name == chartPath:
			result.ChartPath = chartPath
			result.HasChartYAML = true
		case matchesExternalChartYAML(externalChartPath, resource.Name):
			result.ChartPath = resource.Name
			result.HasChartYAML = true
		case resource.Name == kustomizePath:
			result.KustomizePath = kustomizePath
		}
	}

	return result
}
