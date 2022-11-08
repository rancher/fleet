package helm

import (
	"path/filepath"
	"strings"

	"helm.sh/helm/v3/pkg/chart"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/bundlereader"
	"github.com/rancher/fleet/pkg/fleetyaml"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/rancher/fleet/pkg/rawyaml"

	"github.com/rancher/wrangler/pkg/kv"

	"sigs.k8s.io/yaml"
)

func Process(name string, m *manifest.Manifest, style bundlereader.Style) (*manifest.Manifest, error) {
	newManifest := toChart(m, style)
	if !style.HasChartYAML {
		return addChartYAML(name, m, newManifest)
	}
	return newManifest, nil
}

func move(m *manifest.Manifest, from, to string) (result []fleet.BundleResource) {
	if from == "." {
		from = ""
	} else if from != "" {
		from += "/"
	}
	for _, resource := range m.Resources {
		if strings.HasPrefix(resource.Name, from) {
			resource.Name = to + strings.TrimPrefix(resource.Name, from)
			result = append(result, resource)
		}
	}
	return result
}

func manifests(m *manifest.Manifest) (result []fleet.BundleResource) {
	var ignorePrefix []string
	for _, resource := range m.Resources {
		if fleetyaml.IsFleetYamlSuffix(resource.Name) ||
			strings.HasSuffix(resource.Name, "/Chart.yaml") {
			ignorePrefix = append(ignorePrefix, filepath.Dir(resource.Name)+"/")
		}
	}

outer:
	for _, resource := range m.Resources {
		if fleetyaml.IsFleetYaml(resource.Name) {
			continue
		}
		if !strings.HasSuffix(resource.Name, ".yaml") &&
			!strings.HasSuffix(resource.Name, ".json") &&
			!strings.HasSuffix(resource.Name, ".yml") {
			continue
		}
		for _, prefix := range ignorePrefix {
			if strings.HasPrefix(resource.Name, prefix) {
				continue outer
			}
		}
		if strings.HasPrefix(resource.Name, "templates/") {
			resource.Name = "chart/" + resource.Name
		} else {
			resource.Name = rawyaml.YAMLPrefix + resource.Name
		}
		result = append(result, resource)
	}

	return result
}

func toChart(m *manifest.Manifest, style bundlereader.Style) *manifest.Manifest {
	var (
		resources []fleet.BundleResource
	)

	if style.ChartPath != "" {
		resources = move(m, filepath.Dir(style.ChartPath), "chart/")
	} else if style.IsRawYAML() {
		resources = manifests(m)
	}

	return &manifest.Manifest{
		Resources: resources,
		Commit:    m.Commit,
	}
}

func addChartYAML(name string, m, newManifest *manifest.Manifest) (*manifest.Manifest, error) {
	_, hash, err := m.Content()
	if err != nil {
		return nil, err
	}

	if newManifest.Commit != "" && len(newManifest.Commit) > 12 {
		hash = "git-" + newManifest.Commit[:12]
	}

	_, chartName := kv.RSplit(name, "/")
	metadata := chart.Metadata{
		Name:       chartName,
		Version:    "v0.0.0+" + hash,
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
