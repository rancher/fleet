// Package target provides functionality around building and deploying bundledeployments. (fleetcontroller)
//
// Each "Target" represents a bundle, cluster pair and will be transformed into a bundledeployment.
// The manifest, persisted in the content resource, contains the resources available to
// these bundledeployments.
package target

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/bundlematcher"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/rancher/fleet/pkg/options"
	"github.com/rancher/fleet/pkg/summary"

	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/yaml"

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

const maxTemplateRecursionDepth = 50

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

func (m *Manager) BundleFromDeployment(bd *fleet.BundleDeployment) (string, string) {
	return bd.Labels["fleet.cattle.io/bundle-namespace"],
		bd.Labels["fleet.cattle.io/bundle-name"]
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
		bm, err := bundlematcher.New(app)
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

func (m *Manager) GetBundleDeploymentsForBundleInCluster(app *fleet.Bundle, cluster *fleet.Cluster) (result []*fleet.BundleDeployment, err error) {
	bundleDeployments, err := m.bundleDeploymentCache.List("", labels.SelectorFromSet(deploymentLabelsForSelector(app)))
	if err != nil {
		return nil, err
	}
	nsPrefix := name.SafeConcatName("cluster", cluster.Namespace, cluster.Name)
	for _, bd := range bundleDeployments {
		if strings.HasPrefix(bd.Namespace, nsPrefix) {
			result = append(result, bd)
		}
	}

	return result, nil
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
		namespaces, err := matcher.Namespaces()
		if err != nil {
			return nil, err
		}
		for _, namespace := range namespaces {
			nses.Insert(namespace.Name)
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
	bm, err := bundlematcher.New(bundle)
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

			opts := options.Merge(bundle.Spec.BundleDeploymentOptions, target.BundleDeploymentOptions)
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
		return
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

	if err := processLabelValues(opts.Helm.Values.Data, clusterLabels); err != nil {
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
			"ClusterLabels":      clusterLabels,
			"ClusterAnnotations": clusterAnnotations,
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

// foldInDeployments adds the existing bundledeployments to the targets.
func (m *Manager) foldInDeployments(bundle *fleet.Bundle, targets []*Target) error {
	bundleDeployments, err := m.bundleDeploymentCache.List("", labels.SelectorFromSet(deploymentLabelsForSelector(bundle)))
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

func deploymentLabelsForNewBundle(bundle *fleet.Bundle) map[string]string {
	labels := yaml.CleanAnnotationsForExport(bundle.Labels)
	for k, v := range bundle.Labels {
		if strings.HasPrefix(k, "fleet.cattle.io/") {
			labels[k] = v
		}
	}
	for k, v := range deploymentLabelsForSelector(bundle) {
		labels[k] = v
	}
	return labels
}

func deploymentLabelsForSelector(bundle *fleet.Bundle) map[string]string {
	return map[string]string{
		"fleet.cattle.io/bundle-name":      bundle.Name,
		"fleet.cattle.io/bundle-namespace": bundle.Namespace,
	}
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

// ResetDeployment replaces the BundleDeployment for the target with a new one
func (t *Target) ResetDeployment() {
	labels := map[string]string{}
	for k, v := range deploymentLabelsForNewBundle(t.Bundle) {
		labels[k] = v
	}
	labels[fleet.ManagedLabel] = "true"
	t.Deployment = &fleet.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      t.Bundle.Name,
			Namespace: t.Cluster.Status.Namespace,
			Labels:    labels,
		},
	}
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

func processTemplateValues(valuesMap map[string]interface{}, templateContext map[string]interface{}) (map[string]interface{}, error) {
	tplFn := template.New("values").Funcs(tplFuncMap()).Option("missingkey=error")
	recursionDepth := 0
	tplResult, err := templateSubstitutions(valuesMap, templateContext, tplFn, recursionDepth)
	if err != nil {
		return nil, err
	}
	compiledYaml, ok := tplResult.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("templated result was expected to be map[string]interface{}, got %T", tplResult)
	}

	return compiledYaml, nil
}

func templateSubstitutions(src interface{}, templateContext map[string]interface{}, tplFn *template.Template, recursionDepth int) (interface{}, error) {
	if recursionDepth > maxTemplateRecursionDepth {
		return nil, fmt.Errorf("maximum recursion depth of %v exceeded for current templating operation, too many nested values", maxTemplateRecursionDepth)
	}

	switch tplVal := src.(type) {
	case string:
		tpl, err := tplFn.Parse(tplVal)
		if err != nil {
			return nil, err
		}

		var tplBytes bytes.Buffer
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("failed to process template substitution for string '%s': [%v]", tplVal, err)
			}
		}()
		err = tpl.Execute(&tplBytes, templateContext)
		if err != nil {
			return nil, fmt.Errorf("failed to process template substitution for string '%s': [%v]", tplVal, err)
		}
		return tplBytes.String(), nil
	case map[string]interface{}:
		newMap := make(map[string]interface{})
		for key, val := range tplVal {
			processedKey, err := templateSubstitutions(key, templateContext, tplFn, recursionDepth+1)
			if err != nil {
				return nil, err
			}
			keyAsString, ok := processedKey.(string)
			if !ok {
				return nil, fmt.Errorf("expected a string to be returned, but instead got [%T]", processedKey)
			}
			if newMap[keyAsString], err = templateSubstitutions(val, templateContext, tplFn, recursionDepth+1); err != nil {
				return nil, err
			}
		}
		return newMap, nil
	case []interface{}:
		newSlice := make([]interface{}, len(tplVal))
		for i, v := range tplVal {
			newVal, err := templateSubstitutions(v, templateContext, tplFn, recursionDepth+1)
			if err != nil {
				return nil, err
			}
			newSlice[i] = newVal
		}
		return newSlice, nil
	default:
		return tplVal, nil
	}
}

func processLabelValues(valuesMap map[string]interface{}, clusterLabels map[string]string) error {
	prefix := "global.fleet.clusterLabels."
	for key, val := range valuesMap {
		valStr, ok := val.(string)
		if ok && strings.HasPrefix(valStr, prefix) {
			label := strings.TrimPrefix(valStr, prefix)
			labelVal, labelPresent := clusterLabels[label]
			if labelPresent {
				valuesMap[key] = labelVal
			} else {
				valuesMap[key] = ""
				logrus.Infof("Cluster label '%s' for key '%s' is missing from some clusters, setting value to empty string for these clusters.", valStr, key)
			}
		}

		if valMap, ok := val.(map[string]interface{}); ok {
			err := processLabelValues(valMap, clusterLabels)
			if err != nil {
				return err
			}
		}

		if valArr, ok := val.([]interface{}); ok {
			for _, item := range valArr {
				if itemMap, ok := item.(map[string]interface{}); ok {
					err := processLabelValues(itemMap, clusterLabels)
					if err != nil {
						return err
					}
				}
			}
		}
	}

	return nil
}
