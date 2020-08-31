package clustergroup

import (
	"context"
	"sort"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/summary"
	"github.com/sirupsen/logrus"
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
	if cluster == nil || len(cluster.Labels) == 0 {
		return cluster, nil
	}

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

	status.Summary = fleet.BundleSummary{}
	status.ClusterCount = 0
	status.NonReadyClusterCount = 0
	status.NonReadyClusters = nil

	sort.Slice(clusters, func(i, j int) bool {
		return clusters[i].Name < clusters[j].Name
	})

	for _, cluster := range clusters {
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
