package target

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/pkg/errors"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/bundle"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/rancher/fleet/pkg/options"
	"github.com/rancher/fleet/pkg/summary"
	"github.com/rancher/wrangler/pkg/data"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/yaml"
	"github.com/sirupsen/logrus"
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

func ClusterGroupsToLabelMap(cgs []*fleet.ClusterGroup) map[string]map[string]string {
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

	if err := bundleSet.insert(m.bundleCache.List(cluster.Namespace, labels.Everything())); err != nil {
		return nil, err
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

func (m *Manager) BundlesForCluster(cluster *fleet.Cluster) (result []*fleet.Bundle, _ error) {
	bundles, err := m.getBundlesInScopeForCluster(cluster)
	if err != nil {
		return nil, err
	}

	for _, app := range bundles {
		bundle, err := bundle.New(app)
		if err != nil {
			logrus.Errorf("ignore bad app %s/%s: %v", app.Namespace, app.Name, err)
			continue
		}

		cgs, err := m.clusterGroupsForCluster(cluster)
		if err != nil {
			return nil, err
		}

		m := bundle.Match(cluster.Name, ClusterGroupsToLabelMap(cgs), cluster.Labels)
		if m != nil {
			result = append(result, app)
		}
	}

	return
}

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

func (m *Manager) Targets(fleetBundle *fleet.Bundle) (result []*Target, _ error) {
	bundle, err := bundle.New(fleetBundle)
	if err != nil {
		return nil, err
	}

	manifest, err := manifest.New(&bundle.Definition.Spec)
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

			match := bundle.Match(cluster.Name, ClusterGroupsToLabelMap(clusterGroups), cluster.Labels)
			if match == nil {
				continue
			}

			opts := options.Calculate(&fleetBundle.Spec, match.Target)
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
				Target:        match.Target,
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
	bundleDeployments, err := m.bundleDeploymentCache.List("", labels.SelectorFromSet(DeploymentLabelsForSelector(app)))
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

func DeploymentLabelsForNewBundle(app *fleet.Bundle) map[string]string {
	labels := yaml.CleanAnnotationsForExport(app.Labels)
	for k, v := range app.Labels {
		if strings.HasPrefix(k, "fleet.cattle.io/") {
			labels[k] = v
		}
	}
	for k, v := range DeploymentLabelsForSelector(app) {
		labels[k] = v
	}
	return labels
}

func DeploymentLabelsForSelector(app *fleet.Bundle) map[string]string {
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
	for k, v := range DeploymentLabelsForNewBundle(t.Bundle) {
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

func Limit(count int, val ...*intstr.IntOrString) (int, error) {
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
	return Limit(len(targets), rollout.MaxUnavailable)
}

func MaxUnavailablePartitions(partitions []Partition, targets []*Target) (int, error) {
	rollout := getRollout(targets)
	return Limit(len(partitions), rollout.MaxUnavailablePartitions, &defMaxUnavailablePartitions)
}

func IsPartitionUnavailable(status *fleet.PartitionStatus, targets []*Target) bool {
	// Unavailable for a partition is stricter than unavailable for a target.
	// For a partition a target must be available and update to date.
	status.Unavailable = 0
	for _, target := range targets {
		if !UpToDate(target) || IsUnavailable(target.Deployment) {
			status.Unavailable++
		}
	}

	return status.Unavailable > status.MaxUnavailable
}

func UpToDate(target *Target) bool {
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

func (t *Target) Modified() []fleet.ModifiedStatus {
	if t.Deployment == nil {
		return nil
	}
	return t.Deployment.Status.ModifiedStatus
}

func (t *Target) NonReady() []fleet.NonReadyStatus {
	if t.Deployment == nil {
		return nil
	}
	return t.Deployment.Status.NonReadyStatus
}

func (t *Target) State() fleet.BundleState {
	switch {
	case t.Deployment == nil:
		return fleet.Pending
	default:
		return summary.GetDeploymentState(t.Deployment)
	}
}

func (t *Target) Message() string {
	return summary.MessageFromDeployment(t.Deployment)
}

func Summary(targets []*Target) fleet.BundleSummary {
	var bundleSummary fleet.BundleSummary
	for _, currentTarget := range targets {
		cluster := currentTarget.Cluster.Namespace + "/" + currentTarget.Cluster.Name
		summary.IncrementState(&bundleSummary, cluster, currentTarget.State(), currentTarget.Message(), currentTarget.Modified(), currentTarget.NonReady())
		bundleSummary.DesiredReady++
	}
	return bundleSummary
}

func processLabelValues(valuesMap map[string]interface{}, clusterLabels map[string]string) error {
	prefix := "global.fleet.clusterLabels."
        scopedClusterLables := map[string]interface{}{
                "global": map[string]interface{}{
                        "fleet": map[string]interface{}{
                                "clusterLabels": clusterLabels,
                        },
                },
        }
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
		} else {
			switch val.(type) {
			case string:
				valuesTemplate, _ := template.New("clusterLabels").Option("missingkey=error").Parse(valStr)
				var tpl bytes.Buffer
				err := valuesTemplate.Execute(&tpl, scopedClusterLables)
				if err == nil {
					valuesMap[key] = tpl.String()
				} else {
					logrus.Errorf("Failed to process template label subsitution for key '%s' with value '%s': [%v]", key, valStr, err)
				}
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
