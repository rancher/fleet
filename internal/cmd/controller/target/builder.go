package target

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/go-logr/logr"

	"github.com/rancher/fleet/internal/cmd/controller/options"
	"github.com/rancher/fleet/internal/cmd/controller/target/matcher"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v2/pkg/yaml"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	kyaml "sigs.k8s.io/yaml"
)

type Manager struct {
	client client.Client
}

func New(client client.Client) *Manager {
	return &Manager{client: client}
}

// Targets returns all targets for a bundle, so we can create bundledeployments for each.
// This is done by checking all namespaces for clusters matching the bundle's
// BundleTarget matchers.
//
// The returned target structs contain merged BundleDeploymentOptions.
// Finally all existing bundledeployments are added to the targets.
func (m *Manager) Targets(ctx context.Context, bundle *fleet.Bundle, manifestID string) ([]*Target, error) {
	logger := log.FromContext(ctx).WithName("targets")

	bm, err := matcher.New(bundle)
	if err != nil {
		return nil, err
	}

	namespaces, err := m.getNamespacesForBundle(ctx, bundle)
	if err != nil {
		return nil, err
	}

	var targets []*Target
	for _, namespace := range namespaces {
		clusters := &fleet.ClusterList{}
		err := m.client.List(ctx, clusters, client.InNamespace(namespace))
		if err != nil {
			return nil, err
		}

		for _, cluster := range clusters.Items {
			cluster := cluster
			logger.V(4).Info("Cluster has namespace?", "cluster", cluster.Name, "namespace", cluster.Status.Namespace)
			clusterGroups, err := m.clusterGroupsForCluster(ctx, &cluster)
			if err != nil {
				return nil, err
			}

			target := bm.Match(cluster.Name, clusterGroupsToLabelMap(clusterGroups), cluster.Labels)
			if target == nil {
				continue
			}
			// check if there is any matching targetCustomization that should be applied
			targetOpts := target.BundleDeploymentOptions
			targetCustomized := bm.MatchTargetCustomizations(cluster.Name, clusterGroupsToLabelMap(clusterGroups), cluster.Labels)
			if targetCustomized != nil {
				if targetCustomized.DoNotDeploy {
					logger.V(1).Info("BundleDeployment creation for Bundle was skipped because doNotDeploy is set to true.")
					continue
				}
				targetOpts = targetCustomized.BundleDeploymentOptions
			}

			opts := options.Merge(bundle.Spec.BundleDeploymentOptions, targetOpts)
			err = preprocessHelmValues(logger, &opts, &cluster)
			if err != nil {
				return nil, err
			}

			deploymentID, err := options.DeploymentID(manifestID, opts)
			if err != nil {
				return nil, err
			}

			targets = append(targets, &Target{
				ClusterGroups: clusterGroups,
				Cluster:       &cluster,
				Bundle:        bundle,
				Options:       opts,
				DeploymentID:  deploymentID,
			})
		}
	}

	sort.Slice(targets, func(i, j int) bool {
		return targets[i].Cluster.Name < targets[j].Cluster.Name
	})

	return targets, m.foldInDeployments(ctx, bundle, targets)
}

// getNamespacesForBundle returns the namespaces that bundledeployments could
// be created in.
// These are the bundle's namespace, e.g. "fleet-local", and every namespace
// matched by a bundle namespace mapping resource.
func (m *Manager) getNamespacesForBundle(ctx context.Context, bundle *fleet.Bundle) ([]string, error) {
	logger := log.FromContext(ctx).WithName("getNamespacesForBundle")
	mappings := &fleet.BundleNamespaceMappingList{}
	err := m.client.List(ctx, mappings, client.InNamespace(bundle.Namespace))
	if err != nil {
		return nil, err
	}

	nses := sets.NewString(bundle.Namespace)
	for _, mapping := range mappings.Items {
		logger.V(4).Info("Looking for matching namespaces", "bundleNamespaceMapping", mapping, "bundle", bundle)
		mapping := mapping // fix gosec warning regarding "Implicit memory aliasing in for loop"
		matcher, err := newBundleMapping(&mapping)
		if err != nil {
			logger.Error(err, "invalid BundleNamespaceMapping skipping", "mappingNamespace", mapping.Namespace, "mappingName", mapping.Name)
			continue
		}
		if matcher.Matches(bundle) {
			namespaces, err := matcher.Namespaces(ctx, m.client)
			if err != nil {
				return nil, err
			}
			for _, namespace := range namespaces {
				nses.Insert(namespace.Name)
			}
		}
	}

	// this is a sorted list
	return nses.List(), nil
}

// foldInDeployments adds the existing bundledeployments to the targets.
func (m *Manager) foldInDeployments(ctx context.Context, bundle *fleet.Bundle, targets []*Target) error {
	bundleDeployments := &fleet.BundleDeploymentList{}
	err := m.client.List(ctx, bundleDeployments, client.MatchingLabels{
		fleet.BundleLabel:          bundle.Name,
		fleet.BundleNamespaceLabel: bundle.Namespace,
	})
	if err != nil {
		return err
	}

	byNamespace := map[string]*fleet.BundleDeployment{}
	for _, bd := range bundleDeployments.Items {
		byNamespace[bd.Namespace] = bd.DeepCopy()
	}

	for _, target := range targets {
		target.Deployment = byNamespace[target.Cluster.Status.Namespace]
	}

	return nil
}

func preprocessHelmValues(logger logr.Logger, opts *fleet.BundleDeploymentOptions, cluster *fleet.Cluster) (err error) {
	clusterLabels := yaml.CleanAnnotationsForExport(cluster.Labels)
	clusterAnnotations := yaml.CleanAnnotationsForExport(cluster.Annotations)

	for k, v := range cluster.Labels {
		if strings.HasPrefix(k, "fleet.cattle.io/") || strings.HasPrefix(k, "management.cattle.io/") {
			clusterLabels[k] = v
		}
	}
	if len(clusterLabels) == 0 {
		return nil
	}

	if opts.Helm == nil {
		opts.Helm = &fleet.HelmOptions{}
		return nil
	}

	opts.Helm = opts.Helm.DeepCopy()
	if opts.Helm.Values == nil || opts.Helm.Values.Data == nil {
		opts.Helm.Values = &fleet.GenericMap{
			Data: map[string]interface{}{},
		}
		return nil
	}

	if err := processLabelValues(logger, opts.Helm.Values.Data, clusterLabels, 0); err != nil {
		return err
	}

	if !opts.Helm.DisablePreProcess {
		templateValues := map[string]interface{}{}
		if cluster.Spec.TemplateValues != nil {
			templateValues = cluster.Spec.TemplateValues.Data
		}

		values := map[string]interface{}{
			"ClusterNamespace":   cluster.Namespace,
			"ClusterName":        cluster.Name,
			"ClusterLabels":      toDict(clusterLabels),
			"ClusterAnnotations": toDict(clusterAnnotations),
			"ClusterValues":      templateValues,
		}

		opts.Helm.Values.Data, err = processTemplateValues(opts.Helm.Values.Data, values)
		if err != nil {
			return err
		}
		logger.V(4).Info("preProcess completed", "releaseName", opts.Helm.ReleaseName)
	}

	return nil

}

// sprig dictionary functions like "default" and "hasKey" expect map[string]interface{}
func toDict(values map[string]string) map[string]interface{} {
	dict := make(map[string]interface{}, len(values))
	for k, v := range values {
		dict[k] = v
	}
	return dict
}

func processLabelValues(logger logr.Logger, valuesMap map[string]interface{}, clusterLabels map[string]string, recursionDepth int) error {
	if recursionDepth > maxTemplateRecursionDepth {
		return fmt.Errorf("maximum recursion depth of %v exceeded for cluster label prefix processing, too many nested values", maxTemplateRecursionDepth)
	}

	for key, val := range valuesMap {
		valStr, ok := val.(string)
		if ok && strings.HasPrefix(valStr, clusterLabelPrefix) {
			label := strings.TrimPrefix(valStr, clusterLabelPrefix)
			labelVal, labelPresent := clusterLabels[label]
			if labelPresent {
				valuesMap[key] = labelVal
			} else {
				valuesMap[key] = ""
				logger.Info("Cluster label for key is missing from some clusters, setting value to empty string for these clusters.", "label", valStr, "key", key)
			}
		}

		if valMap, ok := val.(map[string]interface{}); ok {
			err := processLabelValues(logger, valMap, clusterLabels, recursionDepth+1)
			if err != nil {
				return err
			}
		}

		if valArr, ok := val.([]interface{}); ok {
			for _, item := range valArr {
				if itemMap, ok := item.(map[string]interface{}); ok {
					err := processLabelValues(logger, itemMap, clusterLabels, recursionDepth+1)
					if err != nil {
						return err
					}
				}
			}
		}
	}

	return nil
}

// tplFuncMap returns a mapping of all of the functions from sprig but removes potentially dangerous operations
func tplFuncMap() template.FuncMap {
	f := sprig.TxtFuncMap()
	delete(f, "env")
	delete(f, "expandenv")
	delete(f, "include")
	delete(f, "tpl")

	return f
}

func processTemplateValues(helmValues map[string]interface{}, templateContext map[string]interface{}) (map[string]interface{}, error) {
	data, err := kyaml.Marshal(helmValues)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal helm values section into a template: %w", err)
	}

	// fleet.yaml must be valid yaml, however '{}[]' are YAML control
	// characters and will be interpreted as JSON data structures. This
	// causes issues when parsing the fleet.yaml so we change the delims
	// for templating to '${ }'
	tmpl := template.New("values").Funcs(tplFuncMap()).Option("missingkey=error").Delims("${", "}")
	tmpl, err = tmpl.Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse helm values template: %w", err)
	}

	var b bytes.Buffer
	err = tmpl.Execute(&b, templateContext)
	if err != nil {
		return nil, fmt.Errorf("failed to render helm values template: %w", err)
	}

	var renderedValues map[string]interface{}
	err = kyaml.Unmarshal(b.Bytes(), &renderedValues)
	if err != nil {
		return nil, fmt.Errorf("failed to interpret rendered template as helm values: %#v, %v", renderedValues, err)
	}

	return renderedValues, nil
}
