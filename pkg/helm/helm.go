package helm

import (
	"strings"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/manifest"
	"helm.sh/helm/v3/pkg/chart"
	"sigs.k8s.io/yaml"
)

func Process(name string, m *manifest.Manifest) (*manifest.Manifest, error) {
	newManifest, foundChartYAML := toChart(m)
	if !foundChartYAML {
		return addChartYAML(name, m, newManifest)
	}
	return newManifest, nil
}

func toChart(m *manifest.Manifest) (*manifest.Manifest, bool) {
	found := false
	newManifest := &manifest.Manifest{}
	for _, resource := range m.Resources {
		if strings.HasPrefix(resource.Name, "manifests/") {
			resource.Name = strings.Replace(resource.Name, "manifests/", "chart/templates/", 1)
		}
		if !strings.HasPrefix(resource.Name, "chart/") {
			continue
		}
		if resource.Name == "chart/Chart.yaml" {
			found = true
		}
		newManifest.Resources = append(newManifest.Resources, resource)
	}
	return newManifest, found
}

func addChartYAML(name string, m, newManifest *manifest.Manifest) (*manifest.Manifest, error) {
	_, hash, err := m.Content()
	if err != nil {
		return nil, err
	}

	metadata := chart.Metadata{
		Name:       name,
		Version:    "v0.1-" + hash,
		APIVersion: "v2",
	}

	chart, err := yaml.Marshal(metadata)
	if err != nil {
		return nil, err
	}

	newManifest.Resources = append(newManifest.Resources, fleet.BundleResource{
		Name:    "chart/Chart.yaml",
		Content: string(chart),
	})

	return newManifest, nil
}
