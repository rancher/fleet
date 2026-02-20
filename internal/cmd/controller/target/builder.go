package target

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"maps"
	"sort"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/fleet/internal/cmd/controller/labelselectors"
	"github.com/rancher/fleet/internal/cmd/controller/options"
	"github.com/rancher/fleet/internal/cmd/controller/target/matcher"
	"github.com/rancher/fleet/internal/helmvalues"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/yaml"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	kyaml "sigs.k8s.io/yaml"
)

type Manager struct {
	client client.Client
	reader client.Reader
}

func New(client client.Client, reader client.Reader) *Manager {
	return &Manager{client: client, reader: reader}
}

// Targets returns all targets for a bundle, so we can create bundledeployments for each.
// This is done by checking all namespaces for clusters matching the bundle's
// BundleTarget matchers.
//
// The returned target structs contain merged BundleDeploymentOptions, which
// includes the "TargetCustomizations" from fleet.yaml.
// Finally all existing bundledeployments are added to the targets.
func (m *Manager) Targets(ctx context.Context, bundle *fleet.Bundle, manifestID string) ([]*Target, error) {
	logger := log.FromContext(ctx).WithName("targets")

	namespaceSelector, err := m.getNamespaceSelectorForBundle(ctx, bundle)
	if err != nil {
		return nil, fmt.Errorf("failed to get namespace selector: %w", err)
	}

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
			logger.V(4).Info("Cluster has namespace?", "cluster", cluster.Name, "namespace", cluster.Status.Namespace)
			clusterGroups, err := m.clusterGroupsForCluster(ctx, &cluster)
			if err != nil {
				return nil, err
			}

			target := bm.Match(cluster.Name, ClusterGroupsToLabelMap(clusterGroups), cluster.Labels)
			if target == nil {
				continue
			}
			// Check all matching targetCustomizations for doNotDeploy, not just the first match.
			// This ensures that a doNotDeploy entry is honoured even when a broader-matching
			// target appears before it in the target list (fixes first-match bypass).
			if bm.HasDoNotDeployTarget(cluster.Name, ClusterGroupsToLabelMap(clusterGroups), cluster.Labels) {
				logger.V(1).Info("BundleDeployment creation for Bundle was skipped because doNotDeploy is set to true.")
				continue
			}
			if target.DoNotDeploy {
				logger.V(1).Info("BundleDeployment creation for Bundle was skipped because doNotDeploy is set to true.")
				continue
			}
			// check if there is any matching targetCustomization that should be applied
			targetOpts := target.BundleDeploymentOptions
			targetCustomized := bm.MatchTargetCustomizations(cluster.Name, ClusterGroupsToLabelMap(clusterGroups), cluster.Labels)
			if targetCustomized != nil {
				targetOpts = targetCustomized.BundleDeploymentOptions
			}

			opts := options.Merge(bundle.Spec.BundleDeploymentOptions, targetOpts)
			if namespaceSelector != nil {
				opts.AllowedTargetNamespaceSelector = namespaceSelector
			}

			err = preprocessHelmValues(logger, &opts, &cluster)
			if err != nil {
				return nil, fmt.Errorf("cluster %s in namespace %s: %w", cluster.Name, cluster.Namespace, err)
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

	// add the existing bundledeployments to the targets.
	bundleDeployments := &fleet.BundleDeploymentList{}
	err = m.client.List(ctx, bundleDeployments, client.MatchingLabels{
		fleet.BundleLabel:          bundle.Name,
		fleet.BundleNamespaceLabel: bundle.Namespace,
	})
	if err != nil {
		return nil, err
	}

	byNamespace := map[string]*fleet.BundleDeployment{}
	for _, bd := range bundleDeployments.Items {
		bd := bd.DeepCopy()
		byNamespace[bd.Namespace] = bd

		// and set their options
		if bd.Spec.ValuesHash != "" {
			secret := &corev1.Secret{}
			if err := m.reader.Get(ctx, client.ObjectKey{Namespace: bd.Namespace, Name: bd.Name}, secret); err != nil {
				if apierrors.IsNotFound(err) {
					logger.V(1).Info("failed to get options secret for bundledeployment %s/%s, this is likely temporary", bd.Namespace, bd.Name)
					continue
				}
				return nil, err
			}

			h := helmvalues.HashOptions(secret.Data[helmvalues.ValuesKey], secret.Data[helmvalues.StagedValuesKey])
			if h != bd.Spec.ValuesHash {
				return nil, fmt.Errorf("retrying, hash mismatch between secret and bundledeployment: actual %s != expected %s", h, bd.Spec.ValuesHash)
			}

			if err := helmvalues.SetOptions(bd, secret.Data); err != nil {
				return nil, err
			}
		}

	}

	for _, target := range targets {
		target.Deployment = byNamespace[target.Cluster.Status.Namespace]
	}

	return targets, err
}

// getNamespacesForBundle returns the namespaces that bundledeployments could
// be created in.
// These are the bundle's namespace, e.g. "fleet-local", and every namespace
// matched by a bundle namespace mapping resource.
func (m *Manager) getNamespacesForBundle(ctx context.Context, bundle *fleet.Bundle) ([]string, error) {
	logger := log.FromContext(ctx).WithName("get-namespaces-for-bundle").WithValues("bundle", bundle)
	mappings := &fleet.BundleNamespaceMappingList{}
	err := m.client.List(ctx, mappings, client.InNamespace(bundle.Namespace))
	if err != nil {
		return nil, err
	}

	nses := sets.NewString(bundle.Namespace)
	for _, mapping := range mappings.Items {
		logger.V(4).Info("Looking for matching namespaces", "bundleNamespaceMapping", mapping)
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
	opts.Helm.Values = cmp.Or(opts.Helm.Values, &fleet.GenericMap{
		Data: map[string]interface{}{},
	})

	if opts.Helm.Values.Data == nil {
		opts.Helm.Values.Data = map[string]interface{}{}
	}

	if opts.Helm.TemplateValues == nil && len(opts.Helm.Values.Data) == 0 {
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

		templatedData, err := processTemplateValuesData(opts.Helm.TemplateValues, values)
		if err != nil {
			return err
		}

		maps.Copy(opts.Helm.Values.Data, templatedData)

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

func processTemplateValuesData(helmTemplateData map[string]string, templateContext map[string]interface{}) (map[string]interface{}, error) {
	renderedValues := make(map[string]interface{}, len(helmTemplateData))

	for k, v := range helmTemplateData {
		// fleet.yaml must be valid yaml, however '{}[]' are YAML control
		// characters and will be interpreted as JSON data structures. This
		// causes issues when parsing the fleet.yaml so we change the delims
		// for templating to '${ }'
		tmpl := template.New("values").Funcs(tplFuncMap()).Option("missingkey=error").Delims("${", "}")
		tmpl, err := tmpl.Parse(v)
		if err != nil {
			return nil, fmt.Errorf("failed to parse helm values template: %w", err)
		}

		var b bytes.Buffer
		err = tmpl.Execute(&b, templateContext)
		if err != nil {
			return nil, fmt.Errorf("failed to render helm values template: %w", err)
		}

		var value interface{}
		err = kyaml.Unmarshal(b.Bytes(), &value)
		if err != nil {
			return nil, fmt.Errorf("failed to interpret rendered template as helm values: %s, %w", b.String(), err)
		}

		renderedValues[k] = value
	}

	return renderedValues, nil
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
		return nil, fmt.Errorf("failed to interpret rendered template as helm values: %#v, %w", renderedValues, err)
	}

	return renderedValues, nil
}

// getNamespaceSelectorForBundle aggregates AllowedTargetNamespaceSelector from all
// GitRepoRestrictions for bundles originating from a GitRepo.
func (m *Manager) getNamespaceSelectorForBundle(ctx context.Context, bundle *fleet.Bundle) (*metav1.LabelSelector, error) {
	if gitRepoLabel := bundle.Labels[fleet.RepoLabel]; gitRepoLabel == "" {
		return nil, nil
	}

	restrictions := &fleet.GitRepoRestrictionList{}
	if err := m.client.List(ctx, restrictions, client.InNamespace(bundle.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list GitRepoRestrictions: %w", err)
	}

	var result *metav1.LabelSelector
	for _, restriction := range restrictions.Items {
		result = labelselectors.Merge(result, restriction.AllowedTargetNamespaceSelector)
	}

	return result, nil
}
