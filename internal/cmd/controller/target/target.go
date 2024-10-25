// Package target provides functionality around building and deploying bundledeployments.
//
// Each "Target" represents a bundle, cluster pair and will be transformed into a bundledeployment.
// The manifest, persisted in the content resource, contains the resources available to
// these bundledeployments.
package target

import (
	"bytes"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	kyaml "sigs.k8s.io/yaml"

	"github.com/rancher/fleet/internal/cmd/controller/options"
	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/internal/cmd/controller/target/matcher"
	"github.com/rancher/fleet/internal/manifest"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"

	corecontrollers "github.com/rancher/wrangler/v2/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/v2/pkg/yaml"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/Masterminds/sprig/v3"
)

var (
	// Default limit is 100%, make sure the default behavior doesn't block rollout
	defLimit                    = intstr.FromString("100%")
	defAutoPartitionSize        = intstr.FromString("25%")
	defMaxUnavailablePartitions = intstr.FromInt(0)
)

const (
	maxTemplateRecursionDepth = 50
	clusterLabelPrefix        = "global.fleet.clusterLabels."
	byBundleIndexerName       = "fleet.byBundle"
)

type Manager struct {
	clusters                    fleetcontrollers.ClusterCache
	clusterGroups               fleetcontrollers.ClusterGroupCache
	bundleDeploymentCache       fleetcontrollers.BundleDeploymentCache
	bundleCache                 fleetcontrollers.BundleCache
	bundleNamespaceMappingCache fleetcontrollers.BundleNamespaceMappingCache
	namespaceCache              corecontrollers.NamespaceCache
	contentStore                manifest.Store
}

func New(
	clusters fleetcontrollers.ClusterCache,
	clusterGroups fleetcontrollers.ClusterGroupCache,
	bundles fleetcontrollers.BundleCache,
	bundleNamespaceMappingCache fleetcontrollers.BundleNamespaceMappingCache,
	namespaceCache corecontrollers.NamespaceCache,
	contentStore manifest.Store,
	bundleDeployments fleetcontrollers.BundleDeploymentCache) *Manager {
	bundleDeployments.AddIndexer(byBundleIndexerName, func(bd *fleet.BundleDeployment) ([]string, error) {
		if bdLabels := bd.GetLabels(); bdLabels != nil {
			bundleNamespace := bdLabels[fleet.BundleNamespaceLabel]
			bundleName := bdLabels[fleet.BundleLabel]
			if bundleNamespace != "" && bundleName != "" {
				return []string{bundleNamespace + "/" + bundleName}, nil
			}
		}
		return nil, nil
	})

	return &Manager{
		clusterGroups:               clusterGroups,
		clusters:                    clusters,
		bundleDeploymentCache:       bundleDeployments,
		bundleNamespaceMappingCache: bundleNamespaceMappingCache,
		bundleCache:                 bundles,
		contentStore:                contentStore,
		namespaceCache:              namespaceCache,
	}
}

// BundleFromDeployment returns the namespace and name of the bundle that
// created the bundledeployment
func (m *Manager) BundleFromDeployment(bd *fleet.BundleDeployment) (string, string) {
	return bd.Labels[fleet.BundleNamespaceLabel],
		bd.Labels[fleet.BundleLabel]
}

// StoreManifest stores the manifest as a content resource and returns the name.
// It copies the resources from the bundle to the content resource.
func (m *Manager) StoreManifest(manifest *manifest.Manifest) (string, error) {
	return m.contentStore.Store(manifest)
}

func clusterGroupsToLabelMap(cgs []*fleet.ClusterGroup) map[string]map[string]string {
	result := map[string]map[string]string{}
	for _, cg := range cgs {
		result[cg.Name] = cg.Labels
	}
	return result
}

func (m *Manager) clusterGroupsForCluster(cluster *fleet.Cluster) (result []*fleet.ClusterGroup, _ error) {
	cgs, err := m.clusterGroups.List(cluster.Namespace, labels.Everything())
	if err != nil {
		return nil, err
	}

	for _, cg := range cgs {
		if cg.Spec.Selector == nil {
			continue
		}
		sel, err := metav1.LabelSelectorAsSelector(cg.Spec.Selector)
		if err != nil {
			logrus.Errorf("invalid selector on clusterGroup %s/%s [%v]: %v", cg.Namespace, cg.Name,
				cg.Spec.Selector, err)
			continue
		}
		if sel.Matches(labels.Set(cluster.Labels)) {
			result = append(result, cg)
		}
	}

	return result, nil
}

func (m *Manager) getBundlesInScopeForCluster(cluster *fleet.Cluster) ([]*fleet.Bundle, error) {
	bundleSet := newBundleSet()

	// all bundles in the cluster namespace are in scope
	// except for agent bundles of other clusters
	bundles, err := m.bundleCache.List(cluster.Namespace, labels.Everything())
	if err != nil {
		return nil, err
	}
	for _, b := range bundles {
		if b.Annotations["objectset.rio.cattle.io/id"] == "fleet-manage-agent" {
			if b.Name == "fleet-agent-"+cluster.Name {
				bundleSet.insertSingle(b)
			}
		} else {
			bundleSet.insertSingle(b)
		}
	}

	mappings, err := m.bundleNamespaceMappingCache.List("", labels.Everything())
	if err != nil {
		return nil, err
	}

	for _, mapping := range mappings {
		matcher, err := NewBundleMapping(mapping, m.namespaceCache, m.bundleCache)
		if err != nil {
			logrus.Errorf("invalid BundleNamespaceMapping %s/%s skipping: %v", mapping.Namespace, mapping.Name, err)
			continue
		}
		if !matcher.MatchesNamespace(cluster.Namespace) {
			continue
		}
		if err := bundleSet.insert(matcher.Bundles()); err != nil {
			return nil, err
		}
	}

	return bundleSet.bundles(), nil
}

func (m *Manager) BundlesForCluster(cluster *fleet.Cluster) (bundlesToRefresh, bundlesToCleanup []*fleet.Bundle, err error) {
	bundles, err := m.getBundlesInScopeForCluster(cluster)
	if err != nil {
		return nil, nil, err
	}

	for _, app := range bundles {
		bm, err := matcher.New(app)
		if err != nil {
			logrus.Errorf("ignore bad app %s/%s: %v", app.Namespace, app.Name, err)
			continue
		}

		cgs, err := m.clusterGroupsForCluster(cluster)
		if err != nil {
			return nil, nil, err
		}

		match := bm.Match(cluster.Name, clusterGroupsToLabelMap(cgs), cluster.Labels)
		if match != nil {
			bundlesToRefresh = append(bundlesToRefresh, app)
		} else {
			bundlesToCleanup = append(bundlesToCleanup, app)
		}
	}

	return
}

func (m *Manager) GetBundleDeploymentsForBundleInCluster(bundle *fleet.Bundle, cluster *fleet.Cluster) (result []*fleet.BundleDeployment, err error) {
	bundleDeployments, err := m.bundleDeploymentCache.GetByIndex(byBundleIndexerName, bundleIndexKey(bundle))
	if err != nil {
		return nil, err
	}
	bundleDeployments = slices.DeleteFunc(bundleDeployments, func(bd *fleet.BundleDeployment) bool {
		if bd == nil || bd.Labels == nil {
			return true
		}
		return bd.Labels[fleet.ClusterLabel] != cluster.Name
	})

	return bundleDeployments, nil
}

// getNamespacesForBundle returns the namespaces that bundledeployments could
// be created in.
// These are the bundle's namespace, e.g. "fleet-local", and every namespace
// matched by a bundle namespace mapping resource.
func (m *Manager) getNamespacesForBundle(bundle *fleet.Bundle) ([]string, error) {
	mappings, err := m.bundleNamespaceMappingCache.List(bundle.Namespace, labels.Everything())
	if err != nil {
		return nil, err
	}

	nses := sets.NewString(bundle.Namespace)
	for _, mapping := range mappings {
		matcher, err := NewBundleMapping(mapping, m.namespaceCache, m.bundleCache)
		if err != nil {
			logrus.Errorf("invalid BundleNamespaceMapping %s/%s skipping: %v", mapping.Namespace, mapping.Name, err)
			continue
		}
		if matcher.Matches(bundle) {
			namespaces, err := matcher.Namespaces()
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

// Targets returns all targets for a bundle, so we can create bundledeployments for each.
// This is done by checking all namespaces for clusters matching the bundle's
// BundleTarget matchers.
//
// The returned target structs contain merged BundleDeploymentOptions.
// Finally all existing bundledeployments are added to the targets.
func (m *Manager) Targets(bundle *fleet.Bundle, manifest *manifest.Manifest) ([]*Target, error) {
	bm, err := matcher.New(bundle)
	if err != nil {
		return nil, err
	}

	namespaces, err := m.getNamespacesForBundle(bundle)
	if err != nil {
		return nil, err
	}

	var targets []*Target
	for _, namespace := range namespaces {
		clusters, err := m.clusters.List(namespace, labels.Everything())
		if err != nil {
			return nil, err
		}

		for _, cluster := range clusters {
			clusterGroups, err := m.clusterGroupsForCluster(cluster)
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
					logrus.Debugf("BundleDeployment creation for Bundle '%s' was skipped because doNotDeploy is set to true.", bundle.Name)
					continue
				}
				targetOpts = targetCustomized.BundleDeploymentOptions
			}

			opts := options.Merge(bundle.Spec.BundleDeploymentOptions, targetOpts)
			err = preprocessHelmValues(&opts, cluster)
			if err != nil {
				return nil, err
			}

			deploymentID, err := options.DeploymentID(manifest, opts)
			if err != nil {
				return nil, err
			}

			targets = append(targets, &Target{
				ClusterGroups: clusterGroups,
				Cluster:       cluster,
				Bundle:        bundle,
				Options:       opts,
				DeploymentID:  deploymentID,
			})
		}
	}

	sort.Slice(targets, func(i, j int) bool {
		return targets[i].Cluster.Name < targets[j].Cluster.Name
	})

	return targets, m.foldInDeployments(bundle, targets)
}

func preprocessHelmValues(opts *fleet.BundleDeploymentOptions, cluster *fleet.Cluster) (err error) {
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

	if err := processLabelValues(opts.Helm.Values.Data, clusterLabels, 0); err != nil {
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
		logrus.Debugf("preProcess completed for %v", opts.Helm.ReleaseName)
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

// foldInDeployments adds the existing bundledeployments to the targets.
func (m *Manager) foldInDeployments(bundle *fleet.Bundle, targets []*Target) error {
	bundleDeployments, err := m.bundleDeploymentCache.GetByIndex(byBundleIndexerName, bundleIndexKey(bundle))
	if err != nil {
		return err
	}

	byNamespace := map[string]*fleet.BundleDeployment{}
	for _, bd := range bundleDeployments {
		byNamespace[bd.Namespace] = bd.DeepCopy()
	}

	for _, target := range targets {
		target.Deployment = byNamespace[target.Cluster.Status.Namespace]
	}

	return nil
}

func bundleIndexKey(bundle *fleet.Bundle) string {
	return bundle.Namespace + "/" + bundle.Name
}

type Target struct {
	Deployment    *fleet.BundleDeployment
	ClusterGroups []*fleet.ClusterGroup
	Cluster       *fleet.Cluster
	Bundle        *fleet.Bundle
	Options       fleet.BundleDeploymentOptions
	DeploymentID  string
}

func (t *Target) IsPaused() bool {
	return t.Cluster.Spec.Paused ||
		t.Bundle.Spec.Paused
}

// BundleDeploymentLabels builds all labels for a bundledeployment
func (t *Target) BundleDeploymentLabels(clusterNamespace string, clusterName string) map[string]string {
	// remove labels starting with kubectl.kubernetes.io or containing
	// cattle.io from bundle
	labels := yaml.CleanAnnotationsForExport(t.Bundle.Labels)

	// copy fleet labels from bundle to bundledeployment
	for k, v := range t.Bundle.Labels {
		if strings.HasPrefix(k, "fleet.cattle.io/") {
			labels[k] = v
		}
	}

	// labels for the bundledeployment by bundle selector
	labels[fleet.BundleLabel] = t.Bundle.Name
	labels[fleet.BundleNamespaceLabel] = t.Bundle.Namespace

	// ManagedLabel allows clean up of the bundledeployment
	labels[fleet.ManagedLabel] = "true"

	// add labels to identify the cluster this bundledeployment belongs to
	labels[fleet.ClusterNamespaceLabel] = clusterNamespace
	labels[fleet.ClusterLabel] = clusterName

	return labels
}

// getRollout returns the rollout strategy for the specified targets (pure function)
func getRollout(targets []*Target) *fleet.RolloutStrategy {
	var rollout *fleet.RolloutStrategy
	if len(targets) > 0 {
		rollout = targets[0].Bundle.Spec.RolloutStrategy
	}
	if rollout == nil {
		rollout = &fleet.RolloutStrategy{}
	}
	return rollout
}

func limit(count int, val ...*intstr.IntOrString) (int, error) {
	if count == 0 {
		return 1, nil
	}

	var maxUnavailable *intstr.IntOrString

	for _, val := range val {
		if val != nil {
			maxUnavailable = val
			break
		}
	}

	if maxUnavailable == nil {
		maxUnavailable = &defLimit
	}

	if maxUnavailable.Type == intstr.Int {
		return maxUnavailable.IntValue(), nil
	}

	i := maxUnavailable.IntValue()
	if i > 0 {
		return i, nil
	}

	if !strings.HasSuffix(maxUnavailable.StrVal, "%") {
		return 0, fmt.Errorf("invalid maxUnavailable, must be int or percentage (ending with %%): %s", maxUnavailable)
	}

	percent, err := strconv.ParseFloat(strings.TrimSuffix(maxUnavailable.StrVal, "%"), 64)
	if err != nil {
		return 0, errors.Wrapf(err, "failed to parse %s", maxUnavailable.StrVal)
	}

	if percent <= 0 {
		return 1, nil
	}

	i = int(float64(count)*percent) / 100
	if i <= 0 {
		return 1, nil
	}

	return i, nil
}

// MaxUnavailable returns the maximum number of unavailable deployments given the targets rollout strategy (pure function)
func MaxUnavailable(targets []*Target) (int, error) {
	rollout := getRollout(targets)
	return limit(len(targets), rollout.MaxUnavailable)
}

// MaxUnavailablePartitions returns the maximum number of unavailable partitions given the targets and partitions (pure function)
func MaxUnavailablePartitions(partitions []Partition, targets []*Target) (int, error) {
	rollout := getRollout(targets)
	return limit(len(partitions), rollout.MaxUnavailablePartitions, &defMaxUnavailablePartitions)
}

// UpdateStatusUnavailable recomputes and sets the status.Unavailable counter and returns true if the partition
// is unavailable, eg. there are more unavailable targets than the maximum set (does not mutate targets)
func UpdateStatusUnavailable(status *fleet.PartitionStatus, targets []*Target) bool {
	// Unavailable for a partition is stricter than unavailable for a target.
	// For a partition a target must be available and update to date.
	status.Unavailable = 0
	for _, target := range targets {
		if !upToDate(target) || IsUnavailable(target.Deployment) {
			status.Unavailable++
		}
	}

	return status.Unavailable > status.MaxUnavailable
}

// upToDate returns true if the target is up to date (pure function)
func upToDate(target *Target) bool {
	if target.Deployment == nil ||
		target.Deployment.Spec.StagedDeploymentID != target.DeploymentID ||
		target.Deployment.Spec.DeploymentID != target.DeploymentID ||
		target.Deployment.Status.AppliedDeploymentID != target.DeploymentID {
		return false
	}

	return true
}

// Unavailable counts the number of targets that are not available (pure function)
func Unavailable(targets []*Target) (count int) {
	for _, target := range targets {
		if target.Deployment == nil {
			continue
		}
		if IsUnavailable(target.Deployment) {
			count++
		}
	}
	return
}

// IsUnavailable checks if target is not available (pure function)
func IsUnavailable(target *fleet.BundleDeployment) bool {
	if target == nil {
		return false
	}
	return target.Status.AppliedDeploymentID != target.Spec.DeploymentID ||
		!target.Status.Ready
}

func (t *Target) modified() []fleet.ModifiedStatus {
	if t.Deployment == nil {
		return nil
	}
	return t.Deployment.Status.ModifiedStatus
}

func (t *Target) nonReady() []fleet.NonReadyStatus {
	if t.Deployment == nil {
		return nil
	}
	return t.Deployment.Status.NonReadyStatus
}

// state calculates a fleet.BundleState from t (pure function)
func (t *Target) state() fleet.BundleState {
	switch {
	case t.Deployment == nil:
		return fleet.Pending
	default:
		return summary.GetDeploymentState(t.Deployment)
	}
}

// message returns a relevant message from the target (pure function)
func (t *Target) message() string {
	return summary.MessageFromDeployment(t.Deployment)
}

// Summary calculates a fleet.BundleSummary from targets (pure function)
func Summary(targets []*Target) fleet.BundleSummary {
	var bundleSummary fleet.BundleSummary
	for _, currentTarget := range targets {
		cluster := currentTarget.Cluster.Namespace + "/" + currentTarget.Cluster.Name
		summary.IncrementState(&bundleSummary, cluster, currentTarget.state(), currentTarget.message(), currentTarget.modified(), currentTarget.nonReady())
		bundleSummary.DesiredReady++
	}
	return bundleSummary
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

func processLabelValues(valuesMap map[string]interface{}, clusterLabels map[string]string, recursionDepth int) error {
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
				logrus.Infof("Cluster label '%s' for key '%s' is missing from some clusters, setting value to empty string for these clusters.", valStr, key)
			}
		}

		if valMap, ok := val.(map[string]interface{}); ok {
			err := processLabelValues(valMap, clusterLabels, recursionDepth+1)
			if err != nil {
				return err
			}
		}

		if valArr, ok := val.([]interface{}); ok {
			for _, item := range valArr {
				if itemMap, ok := item.(map[string]interface{}); ok {
					err := processLabelValues(itemMap, clusterLabels, recursionDepth+1)
					if err != nil {
						return err
					}
				}
			}
		}
	}

	return nil
}
