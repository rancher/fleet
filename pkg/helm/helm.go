package helm

import (
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/manifest"
	"helm.sh/helm/v3/pkg/chart"
)

func Process(name string, m *manifest.Manifest) (*manifest.Manifest, error) {
	chartYAML := findChartYAML(m)
	if chartYAML == "" {
		return addChartYAML(name, m)
	}
	return m, nil
}

func findChartYAML(m *manifest.Manifest) string {
	chartYAML := ""
	for _, resource := range m.Resources {
		if strings.HasSuffix(resource.Name, "Chart.yaml") {
			if chartYAML == "" || len(resource.Name) < len(chartYAML) {
				chartYAML = resource.Name
			}
		}
	}
	return chartYAML
}

func addChartYAML(deploymentID string, m *manifest.Manifest) (*manifest.Manifest, error) {
	_, hash, err := m.Content()
	if err != nil {
		return nil, err
	}

	metadata := chart.Metadata{
		Name:       deploymentID,
		Version:    "v0.1-" + hash,
		APIVersion: "v2",
		Annotations: map[string]string{
			"deploymentID": deploymentID,
			"digest":       hash,
		},
	}

	chart, err := yaml.Marshal(metadata)
	if err != nil {
		return nil, err
	}

	newManifest := &manifest.Manifest{
		Resources: []fleet.BundleResource{
			{
				Name:    "Chart.yaml",
				Content: string(chart),
			},
		},
	}

	for _, resource := range m.Resources {
		if strings.HasPrefix(resource.Name, "post/") {
			continue
		}
		if strings.HasPrefix(resource.Name, "templates/") {
			newManifest.Resources = append(newManifest.Resources, resource)
		} else {
			newManifest.Resources = append(newManifest.Resources, fleet.BundleResource{
				Name:     filepath.Join("templates", resource.Name),
				Content:  resource.Content,
				Encoding: resource.Encoding,
			})
		}
	}

	return newManifest, nil
}
