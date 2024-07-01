package render

import (
	"io"
	"path/filepath"
	"strings"

	"helm.sh/helm/v3/pkg/chart"

	"github.com/rancher/fleet/internal/bundlereader"
	"github.com/rancher/fleet/internal/fleetyaml"
	"github.com/rancher/fleet/internal/helmdeployer/rawyaml"
	"github.com/rancher/fleet/internal/helmdeployer/render/patch"
	"github.com/rancher/fleet/internal/manifest"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/kv"

	"sigs.k8s.io/yaml"
)

// HelmChart applies overlays to "manifest"-style gitrepos and transforms the
// manifest into a helm chart tgz
func HelmChart(name string, m *manifest.Manifest, options fleet.BundleDeploymentOptions) (io.Reader, error) {
	var (
		style = bundlereader.DetermineStyle(m, options)
		err   error
	)

	if style.IsRawYAML() {
		var overlays []string
		if options.YAML != nil {
			overlays = options.YAML.Overlays
		}
		m, err = patch.Process(m, overlays)
		if err != nil {
			return nil, err
		}
	}

	m, err = process(name, m, style)
	if err != nil {
		return nil, err
	}

	return m.ToTarGZ()
}

// process filters the manifests resources and adds a Chart.yaml if missing
func process(name string, m *manifest.Manifest, style bundlereader.Style) (*manifest.Manifest, error) {
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

// manifests returns a filtered list of BundleResources
// It also treats the 'templates/' directory as a special case.
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
	manifestID, err := m.ID()
	if err != nil {
		return nil, err
	}

	if newManifest.Commit != "" && len(newManifest.Commit) > 12 {
		manifestID = "git-" + newManifest.Commit[:12]
	}

	_, chartName := kv.RSplit(name, "/")
	metadata := chart.Metadata{
		Name:       chartName,
		Version:    "v0.0.0+" + manifestID,
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
