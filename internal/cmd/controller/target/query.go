package target

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/rancher/fleet/internal/cmd/controller/target/matcher"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func (m *Manager) BundlesForCluster(ctx context.Context, cluster *fleet.Cluster) (bundlesToRefresh, bundlesToCleanup []*fleet.Bundle, err error) {
	bundles, err := m.getBundlesInScopeForCluster(ctx, cluster)
	if err != nil {
		return nil, nil, err
	}

	cgs, err := m.clusterGroupsForCluster(ctx, cluster)
	if err != nil {
		return nil, nil, err
	}

	cgLabelMap := ClusterGroupsToLabelMap(cgs)
	logger := log.FromContext(ctx).WithName("target")
	for _, bundle := range bundles {
		bm, err := matcher.New(bundle)
		if err != nil {
			logger.Error(err, "ignore bad app bundle", "namespace", bundle.Namespace, "name", bundle.Name)
			continue
		}

		match := bm.Match(cluster.Name, cgLabelMap, cluster.Labels)
		if match != nil {
			bundlesToRefresh = append(bundlesToRefresh, bundle)
		} else {
			bundlesToCleanup = append(bundlesToCleanup, bundle)
		}
	}

	return
}

func (m *Manager) getBundlesInScopeForCluster(ctx context.Context, cluster *fleet.Cluster) ([]*fleet.Bundle, error) {
	bundleSet := newBundleSet()

	// all bundles in the cluster namespace are in scope
	// except for agent bundles of other clusters
	bundles := &fleet.BundleList{}
	err := m.client.List(ctx, bundles, client.InNamespace(cluster.Namespace))
	if err != nil {
		return nil, err
	}
	for _, b := range bundles.Items {
		if b.Annotations["objectset.rio.cattle.io/id"] == "fleet-manage-agent" {
			if b.Name == "fleet-agent-"+cluster.Name {
				bundleSet.insertSingle(&b)
			}
		} else {
			bundleSet.insertSingle(&b)
		}
	}
	mappings := &fleet.BundleNamespaceMappingList{}
	err = m.client.List(ctx, mappings)
	if err != nil {
		return nil, err
	}

	logger := log.FromContext(ctx).WithName("target")
	for _, mapping := range mappings.Items {
		matcher, err := newBundleMapping(&mapping)
		if err != nil {
			logger.Error(err, "invalid BundleNamespaceMapping, skipping", "namespace", mapping.Namespace, "name", mapping.Name)
			continue
		}
		if !matcher.MatchesNamespace(ctx, m.client, cluster.Namespace) {
			continue
		}
		if err := bundleSet.insert(matcher.Bundles(ctx, m.client)); err != nil {
			return nil, err
		}
	}

	return bundleSet.bundles(), nil
}

func (m *Manager) clusterGroupsForCluster(ctx context.Context, cluster *fleet.Cluster) (result []*fleet.ClusterGroup, _ error) {
	return ClusterGroupsForCluster(ctx, m.client, cluster, &m.selectorCache)
}

// ClusterGroupsForCluster returns all cluster groups that match the given cluster.
// It always performs a cache retention at the end
func ClusterGroupsForCluster(ctx context.Context, c client.Client, cluster *fleet.Cluster, cache *SelectorCache) (result []*fleet.ClusterGroup, _ error) {
	cgs := &fleet.ClusterGroupList{}
	err := c.List(ctx, cgs, client.InNamespace(cluster.Namespace))
	if err != nil {
		return nil, err
	}

	logger := log.FromContext(ctx).WithName("target")
	active := make(map[string]struct{}, len(cgs.Items))
	for _, cg := range cgs.Items {
		if cg.Spec.Selector == nil {
			continue
		}

		var sel labels.Selector
		if cached, ok := cache.Get(cg.Namespace, cg.Name, cg.ResourceVersion); ok {
			sel = cached
		} else {
			sel, err = metav1.LabelSelectorAsSelector(cg.Spec.Selector)
			if err != nil {
				logger.Error(err, "invalid selector on clusterGroup", "namespace", cg.Namespace, "name", cg.Name,
					"selector", cg.Spec.Selector)
				continue
			}

			cache.Set(cg.Namespace, cg.Name, cg.ResourceVersion, sel)
		}
		// Only ClusterGroups with a usable selector keep their cache entries; entries from a
		// previously valid selector are purged once it becomes nil or invalid.
		active[cg.Name] = struct{}{}
		if sel.Matches(labels.Set(cluster.Labels)) {
			result = append(result, &cg)
		}
	}
	cache.Retain(cluster.Namespace, active)
	return result, nil
}

func ClusterGroupsToLabelMap(cgs []*fleet.ClusterGroup) map[string]map[string]string {
	result := map[string]map[string]string{}
	for _, cg := range cgs {
		result[cg.Name] = cg.Labels
	}
	return result
}
