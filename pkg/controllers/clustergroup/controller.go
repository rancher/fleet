// Package clustergroup provides a controller to update the ClusterGroup resource status. (fleetcontroller)
package clustergroup

import (
	"context"
	"sort"

	"github.com/sirupsen/logrus"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/summary"

	"github.com/rancher/wrangler/pkg/kv"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

type handler struct {
	clusterGroupsCache fleetcontrollers.ClusterGroupCache
	clusterGroups      fleetcontrollers.ClusterGroupController
	clusterCache       fleetcontrollers.ClusterCache
	clusters           fleetcontrollers.ClusterController
}

func Register(ctx context.Context,
	clusters fleetcontrollers.ClusterController,
	clusterGroups fleetcontrollers.ClusterGroupController) {

	h := &handler{
		clusterGroupsCache: clusterGroups.Cache(),
		clusterGroups:      clusterGroups,
		clusterCache:       clusters.Cache(),
		clusters:           clusters,
	}

	fleetcontrollers.RegisterClusterGroupStatusHandler(ctx,
		clusterGroups,
		"Processed",
		"cluster-group",
		h.OnClusterGroup)
	clusters.OnChange(ctx, "cluster-group-trigger", h.OnClusterChange)
}

func (h *handler) OnClusterChange(key string, cluster *fleet.Cluster) (*fleet.Cluster, error) {
	if cluster == nil {
		logrus.Debugf("Cluster '%s' was deleted, enqueue all cluster groups", key)
		ns, _ := kv.Split(key, "/")
		cgs, err := h.clusterGroupsCache.List(ns, labels.Everything())
		if err != nil {
			return nil, err
		}
		for _, cg := range cgs {
			h.clusterGroups.Enqueue(cg.Namespace, cg.Name)
		}
		return cluster, nil
	}

	logrus.Debugf("Cluster '%s' changed, enqueue matching cluster groups", key)

	cgs, err := h.clusterGroupsCache.List(cluster.Namespace, labels.Everything())
	if err != nil {
		return nil, err
	}

	for _, cg := range cgs {
		if cg.Spec.Selector == nil {
			continue
		}
		sel, err := metav1.LabelSelectorAsSelector(cg.Spec.Selector)
		if err != nil {
			logrus.Errorf("invalid selector on clustergroup %s/%s: %v", cg.Namespace, cg.Name, err)
			continue
		}
		if sel.Matches(labels.Set(cluster.Labels)) {
			h.clusterGroups.Enqueue(cg.Namespace, cg.Name)
		}
		// if cluster is removed from CG, need to reconcile if ClusterCount doesnt match
		clusters, err := h.clusterCache.List(cg.Namespace, sel)
		if err != nil {
			logrus.Errorf("error fetching clusters in clustergroup %s%s: %v", cg.Namespace, cg.Name, err)
		}
		if cg.Status.ClusterCount != len(clusters) {
			h.clusterGroups.Enqueue(cg.Namespace, cg.Name)
		}
	}

	return cluster, nil
}

func (h *handler) OnClusterGroup(clusterGroup *fleet.ClusterGroup, status fleet.ClusterGroupStatus) (fleet.ClusterGroupStatus, error) {
	var clusters []*fleet.Cluster
	if clusterGroup.Spec.Selector != nil {
		sel, err := metav1.LabelSelectorAsSelector(clusterGroup.Spec.Selector)
		if err != nil {
			return status, err
		}

		clusters, err = h.clusterCache.List(clusterGroup.Namespace, sel)
		if err != nil {
			return status, err
		}
	}

	logrus.Debugf("ClusterGroupStatusHandler for '%s/%s', updating its status summary", clusterGroup.Namespace, clusterGroup.Name)

	status.Summary = fleet.BundleSummary{}
	status.ResourceCounts = fleet.GitRepoResourceCounts{}
	status.ClusterCount = 0
	status.NonReadyClusterCount = 0
	status.NonReadyClusters = nil

	sort.Slice(clusters, func(i, j int) bool {
		return clusters[i].Name < clusters[j].Name
	})

	for _, cluster := range clusters {
		summary.IncrementResourceCounts(&status.ResourceCounts, cluster.Status.ResourceCounts)
		summary.Increment(&status.Summary, cluster.Status.Summary)
		status.ClusterCount++
		if !summary.IsReady(cluster.Status.Summary) {
			status.NonReadyClusterCount++
			if len(status.NonReadyClusters) < 10 {
				status.NonReadyClusters = append(status.NonReadyClusters, cluster.Name)
			}
		}
	}

	summary.SetReadyConditions(&status, "Bundle", status.Summary)
	return status, nil
}
