package target

import (
	"context"

	"github.com/sirupsen/logrus"
	"sigs.k8s.io/controller-runtime/pkg/client"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/fleet/internal/cmd/controller/target/matcher"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func (m *Manager) BundlesForCluster(ctx context.Context, cluster *fleet.Cluster) (bundlesToRefresh, bundlesToCleanup []*fleet.Bundle, err error) {
	bundles, err := m.getBundlesInScopeForCluster(ctx, cluster)
	if err != nil {
		return nil, nil, err
	}

	for _, bundle := range bundles {
		bm, err := matcher.New(bundle)
		if err != nil {
			logrus.Errorf("ignore bad app %s/%s: %v", bundle.Namespace, bundle.Name, err)
			continue
		}

		cgs, err := m.clusterGroupsForCluster(ctx, cluster)
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
		b := b
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

	for _, mapping := range mappings.Items {
		mapping := mapping
		matcher, err := NewBundleMapping(&mapping)
		if err != nil {
			logrus.Errorf("invalid BundleNamespaceMapping %s/%s skipping: %v", mapping.Namespace, mapping.Name, err)
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
	cgs := &fleet.ClusterGroupList{}
	err := m.client.List(ctx, cgs, client.InNamespace(cluster.Namespace))
	if err != nil {
		return nil, err
	}

	for _, cg := range cgs.Items {
		cg := cg
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
			result = append(result, &cg)
		}
	}

	return result, nil
}

func clusterGroupsToLabelMap(cgs []*fleet.ClusterGroup) map[string]map[string]string {
	result := map[string]map[string]string{}
	for _, cg := range cgs {
		result[cg.Name] = cg.Labels
	}
	return result
}
