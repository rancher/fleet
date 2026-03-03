// Copyright (c) 2024-2026 SUSE LLC

package reconciler

import (
	"context"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/internal/cmd/controller/target/matcher"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// BundleQuery interface for mapping clusters to bundles
// Copied from internal/cmd/controller/reconciler/bundle_controller.go
type BundleQuery interface {
	// BundlesForCluster is used to map from a cluster to bundles
	// Returns: bundlesToRefresh, bundlesToCleanup, error
	BundlesForCluster(context.Context, *fleet.Cluster) ([]*fleet.Bundle, []*fleet.Bundle, error)
}

// bundleQueryImpl implements BundleQuery for the monitor
type bundleQueryImpl struct {
	client client.Client
}

// NewBundleQuery creates a new BundleQuery implementation
func NewBundleQuery(c client.Client) BundleQuery {
	return &bundleQueryImpl{client: c}
}

// BundlesForCluster returns bundles affected by cluster changes
// Adapted from internal/cmd/controller/target/query.go:16-44
func (q *bundleQueryImpl) BundlesForCluster(ctx context.Context, cluster *fleet.Cluster) (bundlesToRefresh, bundlesToCleanup []*fleet.Bundle, err error) {
	bundles, err := q.getBundlesInScopeForCluster(ctx, cluster)
	if err != nil {
		return nil, nil, err
	}

	logger := log.FromContext(ctx).WithName("bundle-query")
	for _, bundle := range bundles {
		bm, err := matcher.New(bundle)
		if err != nil {
			logger.Error(err, "ignore bad app bundle", "namespace", bundle.Namespace, "name", bundle.Name)
			continue
		}

		cgs, err := q.clusterGroupsForCluster(ctx, cluster)
		if err != nil {
			return nil, nil, err
		}

		match := bm.Match(cluster.Name, clusterGroupsToLabelMap(cgs), cluster.Labels)
		if match != nil {
			bundlesToRefresh = append(bundlesToRefresh, bundle)
		} else {
			bundlesToCleanup = append(bundlesToCleanup, bundle)
		}
	}

	return
}

// getBundlesInScopeForCluster returns all bundles that could target this cluster
// Adapted from internal/cmd/controller/target/query.go:46-89
func (q *bundleQueryImpl) getBundlesInScopeForCluster(ctx context.Context, cluster *fleet.Cluster) ([]*fleet.Bundle, error) {
	bundleSet := newBundleSet()

	// All bundles in the cluster namespace are in scope
	// except for agent bundles of other clusters
	bundles := &fleet.BundleList{}
	err := q.client.List(ctx, bundles, client.InNamespace(cluster.Namespace))
	if err != nil {
		return nil, err
	}
	for _, b := range bundles.Items {
		b := b
		if b.Annotations["objectset.rio.cattle.io/id"] == "fleet-manage-agent" {
			if b.Name == "fleet-agent-"+cluster.Name {
				bundleSet.insertSingle(&b)
			}
		} else {
			bundleSet.insertSingle(&b)
		}
	}

	// Handle BundleNamespaceMapping for cross-namespace bundles
	mappings := &fleet.BundleNamespaceMappingList{}
	err = q.client.List(ctx, mappings)
	if err != nil {
		return nil, err
	}

	logger := log.FromContext(ctx).WithName("bundle-query")
	for _, mapping := range mappings.Items {
		mapping := mapping
		matcher, err := newBundleMapping(&mapping)
		if err != nil {
			logger.Error(err, "invalid BundleNamespaceMapping, skipping", "namespace", mapping.Namespace, "name", mapping.Name)
			continue
		}
		if !matcher.MatchesNamespace(ctx, q.client, cluster.Namespace) {
			continue
		}
		if err := bundleSet.insert(matcher.Bundles(ctx, q.client)); err != nil {
			return nil, err
		}
	}

	return bundleSet.bundles(), nil
}

// clusterGroupsForCluster returns ClusterGroups that match this cluster
// Adapted from internal/cmd/controller/target/query.go:91-116
func (q *bundleQueryImpl) clusterGroupsForCluster(ctx context.Context, cluster *fleet.Cluster) (result []*fleet.ClusterGroup, _ error) {
	cgs := &fleet.ClusterGroupList{}
	err := q.client.List(ctx, cgs, client.InNamespace(cluster.Namespace))
	if err != nil {
		return nil, err
	}

	logger := log.FromContext(ctx).WithName("bundle-query")
	for _, cg := range cgs.Items {
		cg := cg
		if cg.Spec.Selector == nil {
			continue
		}
		sel, err := metav1.LabelSelectorAsSelector(cg.Spec.Selector)
		if err != nil {
			logger.Error(err, "invalid selector on clusterGroup", "namespace", cg.Namespace, "name", cg.Name,
				"selector", cg.Spec.Selector)
			continue
		}
		if sel.Matches(labels.Set(cluster.Labels)) {
			result = append(result, &cg)
		}
	}

	return result, nil
}

// clusterGroupsToLabelMap converts cluster groups to label map format
// Copied from internal/cmd/controller/target/query.go:118-124
func clusterGroupsToLabelMap(cgs []*fleet.ClusterGroup) map[string]map[string]string {
	result := map[string]map[string]string{}
	for _, cg := range cgs {
		result[cg.Name] = cg.Labels
	}
	return result
}

// Bundle set helper - adapted from internal/cmd/controller/target/mapping.go:95-130

type bundleSet struct {
	bundleKeys sets.Set[string]
	bundleMap  map[string]*fleet.Bundle
}

func newBundleSet() *bundleSet {
	return &bundleSet{
		bundleKeys: sets.New[string](),
		bundleMap:  map[string]*fleet.Bundle{},
	}
}

func (b *bundleSet) bundles() []*fleet.Bundle {
	var result []*fleet.Bundle
	// list is sorted
	for _, key := range sets.List(b.bundleKeys) {
		result = append(result, b.bundleMap[key])
	}
	return result
}

func (b *bundleSet) insert(bundles []*fleet.Bundle, err error) error {
	if err != nil {
		return err
	}
	for _, bundle := range bundles {
		b.insertSingle(bundle)
	}
	return nil
}

func (b *bundleSet) insertSingle(bundle *fleet.Bundle) {
	key := bundle.Namespace + "/" + bundle.Name
	b.bundleMap[key] = bundle
	b.bundleKeys.Insert(key)
}

// BundleMapping helper - adapted from internal/cmd/controller/target/mapping.go:16-93

// BundleMapping is created from a BundleNamespaceMapping resource
type BundleMapping struct {
	namespace         string
	namespaceSelector labels.Selector
	bundleSelector    labels.Selector
	noMatch           bool
}

func newBundleMapping(mapping *fleet.BundleNamespaceMapping) (*BundleMapping, error) {
	var (
		result = &BundleMapping{
			namespace: mapping.Namespace,
		}
		err error
	)

	if mapping.BundleSelector == nil || mapping.NamespaceSelector == nil {
		result.noMatch = true
		return result, nil
	}

	result.bundleSelector, err = metav1.LabelSelectorAsSelector(mapping.BundleSelector)
	if err != nil {
		return nil, err
	}

	result.namespaceSelector, err = metav1.LabelSelectorAsSelector(mapping.NamespaceSelector)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (b *BundleMapping) Bundles(ctx context.Context, c client.Client) ([]*fleet.Bundle, error) {
	if b.noMatch {
		return nil, nil
	}
	list := &fleet.BundleList{}
	err := c.List(ctx, list, client.InNamespace(b.namespace), client.MatchingLabelsSelector{Selector: b.bundleSelector})

	bundles := make([]*fleet.Bundle, len(list.Items))
	for i := range list.Items {
		bundles[i] = &list.Items[i]
	}
	return bundles, err
}

func (b *BundleMapping) MatchesNamespace(ctx context.Context, c client.Client, namespace string) bool {
	if b.noMatch {
		return false
	}
	ns := &corev1.Namespace{}
	err := c.Get(ctx, types.NamespacedName{Name: namespace}, ns)
	if err != nil {
		return false
	}
	return b.namespaceSelector.Matches(labels.Set(ns.Labels))
}
