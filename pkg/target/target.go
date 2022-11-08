// Package target provides a functions to match bundles and clusters and to list the bundledeployments for that match. (fleetcontroller)
package target

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/bundlematcher"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/rancher/fleet/pkg/options"
	"github.com/rancher/fleet/pkg/summary"

	"github.com/rancher/wrangler/pkg/data"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/yaml"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
)

var (
	// Default limit is 100%, make sure the default behavior doesn't block rollout
	defLimit                    = intstr.FromString("100%")
	defAutoPartitionSize        = intstr.FromString("25%")
	defMaxUnavailablePartitions = intstr.FromInt(0)
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

// getNamespacesForBundle returns the namespaces that the bundle should be
// deployed to. Which is the bundles namespace and every namespace from the
// bundle's namespace mappings.
func (m *Manager) getNamespacesForBundle(fleetBundle *fleet.Bundle) ([]string, error) {
	mappings, err := m.bundleNamespaceMappingCache.List(fleetBundle.Namespace, labels.Everything())
	if err != nil {
		return nil, err
	}

	nses := sets.NewString(fleetBundle.Namespace)
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

// Targets returns all targets for a bundle, so we can create bundledeployments for each
func (m *Manager) Targets(fleetBundle *fleet.Bundle) (result []*Target, _ error) {
	bm, err := bundlematcher.New(fleetBundle)
	if err != nil {
		return nil, err
	}

	manifest, err := manifest.New(&fleetBundle.Spec)
	if err != nil {
		return nil, err
	}

	if _, err := m.contentStore.Store(manifest); err != nil {
		return nil, err
	}

	namespaces, err := m.getNamespacesForBundle(fleetBundle)
	if err != nil {
		return nil, err
	}

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

			opts := options.Merge(fleetBundle.Spec.BundleDeploymentOptions, target.BundleDeploymentOptions)
			err = addClusterLabels(&opts, cluster.Labels)
			if err != nil {
				return nil, err
			}

			deploymentID, err := options.DeploymentID(manifest, opts)
			if err != nil {
				return nil, err
			}

			result = append(result, &Target{
				ClusterGroups: clusterGroups,
				Cluster:       cluster,
				Target:        target,
				Bundle:        fleetBundle,
				Options:       opts,
				DeploymentID:  deploymentID,
			})
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Cluster.Name < result[j].Cluster.Name
	})

	return result, m.foldInDeployments(fleetBundle, result)
}

func addClusterLabels(opts *fleet.BundleDeploymentOptions, labels map[string]string) (err error) {
	clusterLabels := yaml.CleanAnnotationsForExport(labels)
	for k, v := range labels {
		if strings.HasPrefix(k, "fleet.cattle.io/") || strings.HasPrefix(k, "management.cattle.io/") {
			clusterLabels[k] = v
		}
	}
	if len(clusterLabels) == 0 {
		return
	}

	newValues := map[string]interface{}{
		"global": map[string]interface{}{
			"fleet": map[string]interface{}{
				"clusterLabels": clusterLabels,
			},
		},
	}

	if opts.Helm == nil {
		opts.Helm = &fleet.HelmOptions{
			Values: &fleet.GenericMap{
				Data: newValues,
			},
		}
		return nil
	}

	opts.Helm = opts.Helm.DeepCopy()
	if opts.Helm.Values == nil || opts.Helm.Values.Data == nil {
		opts.Helm.Values = &fleet.GenericMap{
			Data: newValues,
		}
		return nil
	}

	if err := processLabelValues(opts.Helm.Values.Data, clusterLabels); err != nil {
		return err
	}

	opts.Helm.Values.Data = data.MergeMaps(opts.Helm.Values.Data, newValues)
	return nil

}

func (m *Manager) foldInDeployments(app *fleet.Bundle, targets []*Target) error {
	bundleDeployments, err := m.bundleDeploymentCache.List("", labels.SelectorFromSet(deploymentLabelsForSelector(app)))
	if err != nil {
		return err
	}

	byNamespace := map[string]*fleet.BundleDeployment{}
	for _, appDep := range bundleDeployments {
		byNamespace[appDep.Namespace] = appDep.DeepCopy()
	}

	for _, target := range targets {
		target.Deployment = byNamespace[target.Cluster.Status.Namespace]
	}

	return nil
}

func deploymentLabelsForNewBundle(app *fleet.Bundle) map[string]string {
	labels := yaml.CleanAnnotationsForExport(app.Labels)
	for k, v := range app.Labels {
		if strings.HasPrefix(k, "fleet.cattle.io/") {
			labels[k] = v
		}
	}
	for k, v := range deploymentLabelsForSelector(app) {
		labels[k] = v
	}
	return labels
}

func deploymentLabelsForSelector(app *fleet.Bundle) map[string]string {
	return map[string]string{
		"fleet.cattle.io/bundle-name":      app.Name,
		"fleet.cattle.io/bundle-namespace": app.Namespace,
	}
}

type Target struct {
	Deployment    *fleet.BundleDeployment
	ClusterGroups []*fleet.ClusterGroup
	Cluster       *fleet.Cluster
	Bundle        *fleet.Bundle
	Target        *fleet.BundleTarget
	Options       fleet.BundleDeploymentOptions
	DeploymentID  string
}

func (t *Target) IsPaused() bool {
	return t.Cluster.Spec.Paused ||
		t.Bundle.Spec.Paused
}

func (t *Target) AssignNewDeployment() {
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

func MaxUnavailable(targets []*Target) (int, error) {
	rollout := getRollout(targets)
	return limit(len(targets), rollout.MaxUnavailable)
}

func MaxUnavailablePartitions(partitions []Partition, targets []*Target) (int, error) {
	rollout := getRollout(targets)
	return limit(len(partitions), rollout.MaxUnavailablePartitions, &defMaxUnavailablePartitions)
}

func IsPartitionUnavailable(status *fleet.PartitionStatus, targets []*Target) bool {
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

func upToDate(target *Target) bool {
	if target.Deployment == nil ||
		target.Deployment.Spec.StagedDeploymentID != target.DeploymentID ||
		target.Deployment.Spec.DeploymentID != target.DeploymentID ||
		target.Deployment.Status.AppliedDeploymentID != target.DeploymentID {
		return false
	}

	return true
}

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

func (t *Target) state() fleet.BundleState {
	switch {
	case t.Deployment == nil:
		return fleet.Pending
	default:
		return summary.GetDeploymentState(t.Deployment)
	}
}

func (t *Target) message() string {
	return summary.MessageFromDeployment(t.Deployment)
}

func Summary(targets []*Target) fleet.BundleSummary {
	var bundleSummary fleet.BundleSummary
	for _, currentTarget := range targets {
		cluster := currentTarget.Cluster.Namespace + "/" + currentTarget.Cluster.Name
		summary.IncrementState(&bundleSummary, cluster, currentTarget.state(), currentTarget.message(), currentTarget.modified(), currentTarget.nonReady())
		bundleSummary.DesiredReady++
	}
	return bundleSummary
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
				return fmt.Errorf("invalid_label_reference %s in key %s", valStr, key)
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
