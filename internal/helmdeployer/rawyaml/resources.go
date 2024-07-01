package rawyaml

import (
	"bytes"
	"strings"

	"helm.sh/helm/v3/pkg/chart"

	"github.com/rancher/wrangler/v3/pkg/yaml"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	YAMLPrefix    = "chart/raw-yaml/"
	inChartPrefix = "raw-yaml/"
)

func ToObjects(c *chart.Chart) (result []runtime.Object, _ error) {
	for _, resource := range c.Files {
		if !strings.HasPrefix(resource.Name, inChartPrefix) {
			continue
		}
		objs, err := yaml.ToObjects(bytes.NewBuffer(resource.Data))
		if err != nil {
			if runtime.IsMissingKind(err) {
				continue
			}
			return nil, err
		}
		for _, obj := range objs {
			apiVersion, kind := obj.GetObjectKind().GroupVersionKind().ToAPIVersionAndKind()
			if apiVersion == "" || kind == "" {
				continue
			}
			result = append(result, obj)
		}
	}

	return result, nil
}
