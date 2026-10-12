package target

import (
	"context"
	"strings"
	"sync"

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

// purgeObsoleteEntries removes cache entries that share the same namespace/name prefix as cacheKey
// but differ in ResourceVersion. This bounds memory growth when a ClusterGroup is updated repeatedly.
func purgeObsoleteEntries(cache *sync.Map, cacheKey string) {
	// cacheKey format: "namespace/name@resourceVersion"
	prefix := cacheKey[:strings.LastIndex(cacheKey, "@")]
	var toPurge []string
	cache.Range(func(k, _ any) bool {
		key := k.(string)
		if key != cacheKey && strings.HasPrefix(key, prefix+"@") {
			toPurge = append(toPurge, key)
		}
		return true
	})

	for _, k := range toPurge {
		cache.Delete(k)
	}
}

// ClusterGroupsForCluster returns all cluster groups that match the given cluster.
func ClusterGroupsForCluster(ctx context.Context, c client.Client, cluster *fleet.Cluster, cache *sync.Map) (result []*fleet.ClusterGroup, _ error) {
	cgs := &fleet.ClusterGroupList{}
	err := c.List(ctx, cgs, client.InNamespace(cluster.Namespace))
	if err != nil {
		return nil, err
	}

	logger := log.FromContext(ctx).WithName("target")
	listed := make(map[string]struct{}, len(cgs.Items))
	for _, cg := range cgs.Items {
		if cg.Spec.Selector == nil {
			continue
		}
		// Cache key includes ResourceVersion so the selector is recompiled when the ClusterGroup changes.
		cacheKey := cg.Namespace + "/" + cg.Name + "@" + cg.ResourceVersion
		var sel labels.Selector
		if cached, ok := loadSelector(cache, cacheKey); ok {
			sel = cached
		} else {
			sel, err = metav1.LabelSelectorAsSelector(cg.Spec.Selector)
			if err != nil {
				logger.Error(err, "invalid selector on clusterGroup", "namespace", cg.Namespace, "name", cg.Name,
					"selector", cg.Spec.Selector)
				continue
			}
			if cache != nil {
				cache.Store(cacheKey, sel)
				purgeObsoleteEntries(cache, cacheKey)
			}
		}
		// Only ClusterGroups with a usable selector keep their cache entries; entries from a
		// previously valid selector are purged once it becomes nil or invalid.
		listed[cg.Name] = struct{}{}
		if sel.Matches(labels.Set(cluster.Labels)) {
			result = append(result, &cg)
		}
	}

	if cache != nil {
		purgeDeletedEntries(cache, cluster.Namespace, listed)
	}

	return result, nil
}

// purgeDeletedEntries removes cache entries in namespace for ClusterGroups which are no longer listed,
// as purgeObsoleteEntries only runs when a ClusterGroup is updated, never when it is deleted.
func purgeDeletedEntries(cache *sync.Map, namespace string, listed map[string]struct{}) {
	prefix := namespace + "/"
	var toPurge []string
	cache.Range(func(k, _ any) bool {
		key := k.(string)
		if !strings.HasPrefix(key, prefix) {
			return true
		}
		// cacheKey format: "namespace/name@resourceVersion"
		name := key[len(prefix):strings.LastIndex(key, "@")]
		if _, ok := listed[name]; !ok {
			toPurge = append(toPurge, key)
		}
		return true
	})

	for _, k := range toPurge {
		cache.Delete(k)
	}
}

func loadSelector(cache *sync.Map, cacheKey string) (labels.Selector, bool) {
	if cache == nil {
		return nil, false
	}
	cached, ok := cache.Load(cacheKey)
	if !ok {
		return nil, false
	}
	return cached.(labels.Selector), true
}

func ClusterGroupsToLabelMap(cgs []*fleet.ClusterGroup) map[string]map[string]string {
	result := map[string]map[string]string{}
	for _, cg := range cgs {
		result[cg.Name] = cg.Labels
	}
	return result
}
