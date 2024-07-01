package patch

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pkg/errors"

	"github.com/rancher/fleet/internal/content"
	"github.com/rancher/fleet/internal/manifest"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/patch"

	"sigs.k8s.io/kustomize/kyaml/yaml"
)

var (
	overlayPrefix = "overlays/"
)

func Process(m *manifest.Manifest, overlays []string) (*manifest.Manifest, error) {
	newManifest := &manifest.Manifest{Commit: m.Commit}
	for i, resource := range m.Resources {
		if resource.Name == "" {
			resource.Name = fmt.Sprintf("manifests/file%03d.yaml", i)
		}
		newManifest.Resources = append(newManifest.Resources, resource)
	}
	m = newManifest

	m, err := patchContext(m, overlays)
	if err != nil {
		return nil, err
	}

	newManifest = &manifest.Manifest{Commit: m.Commit}
	for _, resource := range m.Resources {
		if strings.HasPrefix(resource.Name, overlayPrefix) {
			continue
		}
		newManifest.Resources = append(newManifest.Resources, resource)
	}

	sort.Slice(newManifest.Resources, func(i, j int) bool {
		return newManifest.Resources[i].Name < newManifest.Resources[j].Name
	})

	return newManifest, nil
}

func patchContext(m *manifest.Manifest, overlays []string) (*manifest.Manifest, error) {
	result := map[string][]byte{}

	if len(overlays) == 0 {
		return m, nil
	}

	for _, resource := range m.Resources {
		data, err := content.Decode(resource.Content, resource.Encoding)
		if err != nil {
			return nil, err
		}

		result[resource.Name] = data
	}

	if err := patchContent(result, overlays); err != nil {
		return nil, err
	}

	resultManifest := &manifest.Manifest{}
	for name, bytes := range result {
		resultManifest.Resources = append(resultManifest.Resources, fleet.BundleResource{
			Name:    name,
			Content: string(bytes),
		})
	}

	return resultManifest, nil
}

func isPatchFile(name string) (string, bool) {
	base := filepath.Base(name)
	if strings.Contains(base, "_patch.") {
		target := strings.Replace(base, "_patch.", ".", 1)
		return filepath.Join(filepath.Dir(name), target), true
	}
	return "", false
}

func patchContent(content map[string][]byte, overlays []string) error {
	for _, overlay := range overlays {
		prefix := overlayPrefix + overlay + "/"
		for name, bytes := range content {
			if !strings.HasPrefix(name, prefix) {
				continue
			}

			name := strings.TrimPrefix(name, prefix)
			target, ok := isPatchFile(name)
			if !ok {
				content[name] = bytes
				continue
			}

			targetContent, ok := content[target]
			if !ok {
				return fmt.Errorf("failed to find base file %s to patch", target)
			}

			targetContent, err := convertToJSON(targetContent)
			if err != nil {
				return errors.Wrapf(err, "failed to convert %s to json", target)
			}

			bytes, err = convertToJSON(bytes)
			if err != nil {
				return errors.Wrapf(err, "failed to convert %s to json", name)
			}

			newBytes, err := patch.Apply(targetContent, bytes)
			if err != nil {
				return errors.Wrapf(err, "failed to patch %s", target)
			}
			content[target] = newBytes
		}
	}

	return nil
}

func convertToJSON(input []byte) ([]byte, error) {
	var data interface{}
	data = map[string]interface{}{}
	if err := yaml.Unmarshal(input, &data); err != nil {
		data = []interface{}{}
		if err := yaml.Unmarshal(input, &data); err != nil {
			return nil, err
		}
	}
	return json.Marshal(data)
}
