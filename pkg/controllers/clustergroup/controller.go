package clustergroup

import (
	"context"
	"sort"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/summary"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type handler struct {
	clusterGroups fleetcontrollers.ClusterGroupCache
	clusterCache  fleetcontrollers.ClusterCache
	clusters      fleetcontrollers.ClusterController
}

func Register(ctx context.Context,
	clusters fleetcontrollers.ClusterController,
	clusterGroups fleetcontrollers.ClusterGroupController) {

	h := &handler{
		clusterGroups: clusterGroups.Cache(),
		clusterCache:  clusters.Cache(),
		clusters:      clusters,
	}

	fleetcontrollers.RegisterClusterGroupStatusHandler(ctx,
		clusterGroups,
		"Processed",
		"cluster-group",
		h.OnClusterGroup)
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
		h.clusters.Enqueue(cluster.Namespace, cluster.Name)

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
