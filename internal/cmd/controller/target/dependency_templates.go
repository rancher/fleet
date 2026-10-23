package target

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/yaml"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newTemplateContext distinguishes the source workspace from the cluster's
// registration namespace, which can differ when using BundleNamespaceMapping.
func newTemplateContext(bundle *fleet.Bundle, cluster *fleet.Cluster) map[string]any {
	labels := yaml.CleanAnnotationsForExport(cluster.Labels)
	for key, value := range cluster.Labels {
		if strings.HasPrefix(key, fleet.FleetLabelPrefix) || strings.HasPrefix(key, fleet.ManagementLabelPrefix) {
			labels[key] = value
		}
	}
	values := map[string]any{
		"ClusterNamespace":   cluster.Namespace,
		"ClusterName":        cluster.Name,
		"ClusterLabels":      toDict(labels),
		"ClusterAnnotations": toDict(yaml.CleanAnnotationsForExport(cluster.Annotations)),
		"ClusterValues":      map[string]any{},
	}
	if cluster.Spec.TemplateValues != nil {
		values["ClusterValues"] = cluster.Spec.TemplateValues.Data
	}
	if name := bundle.Labels[fleet.RepoLabel]; name != "" {
		values["GitRepo"] = map[string]any{"Name": name, "Namespace": bundle.Namespace}
	}
	return values
}

// preprocessDependencies renders only matchLabels values. Work on copies so the
// Bundle retains its templates and different clusters cannot affect each other.
// This is independent of helm.disablePreProcess, which controls Helm values.
func preprocessDependencies(dependencies []fleet.BundleRef, context map[string]any) ([]fleet.BundleRef, error) {
	if dependencies == nil {
		return nil, nil
	}
	result := make([]fleet.BundleRef, len(dependencies))
	for i := range dependencies {
		dependencies[i].DeepCopyInto(&result[i])
		selector := result[i].Selector
		if selector == nil {
			continue
		}
		rendered := false
		for key, value := range selector.MatchLabels {
			if !strings.Contains(value, "${") {
				continue
			}
			value, err := renderTemplate(value, context)
			if err != nil {
				return nil, fmt.Errorf("dependsOn[%d].selector.matchLabels[%q]: %w", i, key, err)
			}
			selector.MatchLabels[key] = value
			rendered = true
		}
		if rendered {
			if _, err := metav1.LabelSelectorAsSelector(selector); err != nil {
				return nil, fmt.Errorf("dependsOn[%d].selector: %w", i, err)
			}
		}
	}
	return result, nil
}

func renderTemplate(value string, context map[string]any) (string, error) {
	tmpl, err := template.New("values").Funcs(tplFuncMap()).Option("missingkey=error").Delims("${", "}").Parse(value)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}
	var result bytes.Buffer
	if err := tmpl.Execute(&result, context); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}
	return result.String(), nil
}
