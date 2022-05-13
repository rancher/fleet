package metrics

import (
	"fmt"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	clusterGroupSubsystem = "cluster_group"
	clusterGroupLabels    = []string{"name", "namespace", "generation", "state"}

	clusterGroupClusterCount = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterGroupSubsystem,
			Name:      "cluster_count",
			Help:      "The count of clusters in this cluster group.",
		},
		clusterGroupLabels,
	)
	clusterGroupNonReadyClusterCount = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterGroupSubsystem,
			Name:      "non_ready_cluster_count",
			Help:      "The count of non ready clusters in this cluster group.",
		},
		clusterGroupLabels,
	)
	clusterGroupResourcesDesiredReady = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterGroupSubsystem,
			Name:      "resource_count_desired_ready",
			Help:      "The count of resources that are desired to be in the Ready state.",
		},
		clusterGroupLabels,
	)
	clusterGroupResourcesMissing = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterGroupSubsystem,
			Name:      "resource_count_missing",
			Help:      "The count of resources that are in a Missing state.",
		},
		clusterGroupLabels,
	)
	clusterGroupResourcesModified = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterGroupSubsystem,
			Name:      "resource_count_modified",
			Help:      "The count of resources that are in a Modified state.",
		},
		clusterGroupLabels,
	)
	clusterGroupResourcesNotReady = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterGroupSubsystem,
			Name:      "resource_count_notready",
			Help:      "The count of resources that are in a NotReady state.",
		},
		clusterGroupLabels,
	)
	clusterGroupResourcesOrphaned = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterGroupSubsystem,
			Name:      "resource_count_orphaned",
			Help:      "The count of resources that are in a Orphaned state.",
		},
		clusterGroupLabels,
	)
	clusterGroupResourcesReady = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterGroupSubsystem,
			Name:      "resource_count_ready",
			Help:      "The count of resources that are in a Ready state.",
		},
		clusterGroupLabels,
	)
	clusterGroupResourcesUnknown = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterGroupSubsystem,
			Name:      "resource_count_unknown",
			Help:      "The count of resources that are in a Unknown state.",
		},
		clusterGroupLabels,
	)
	clusterGroupResourcesWaitApplied = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterGroupSubsystem,
			Name:      "resource_count_waitapplied",
			Help:      "The count of resources that are in a WaitApplied state.",
		},
		clusterGroupLabels,
	)
	clusterGroupDesiredReadyBundles = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterGroupSubsystem,
			Name:      "bundle_desired_ready",
			Help:      "The count of bundles that are desired to be in a Ready state.",
		},
		clusterGroupLabels,
	)
	clusterGroupReadyBundles = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterGroupSubsystem,
			Name:      "bundle_ready",
			Help:      "The count of bundles that are in a Ready state in the Cluster Group.",
		},
		clusterGroupLabels,
	)
	clusterGroupState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterGroupSubsystem,
			Name:      "state",
			Help:      "The current state of a given cluster group.",
		},
		clusterGroupLabels,
	)
	clusterGroupObserved = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: clusterGroupSubsystem,
			Name:      "cluster_group_observed_total",
			Help:      "The total times that this cluster group has been observed",
		},
		clusterGroupLabels,
	)
)

func CollectClusterGroupMetrics(clusterGroup *fleet.ClusterGroup, status *fleet.ClusterGroupStatus) {
	labels := prometheus.Labels{
		"name":       clusterGroup.Name,
		"namespace":  clusterGroup.Namespace,
		"generation": fmt.Sprintf("%d", clusterGroup.ObjectMeta.Generation),
		"state":      status.Display.State,
	}

	clusterGroupClusterCount.With(labels).Set(float64(status.ClusterCount))
	clusterGroupNonReadyClusterCount.With(labels).Set(float64(status.NonReadyClusterCount))
	clusterGroupResourcesDesiredReady.With(labels).Set(float64(status.ResourceCounts.DesiredReady))
	clusterGroupResourcesMissing.With(labels).Set(float64(status.ResourceCounts.Missing))
	clusterGroupResourcesModified.With(labels).Set(float64(status.ResourceCounts.Modified))
	clusterGroupResourcesNotReady.With(labels).Set(float64(status.ResourceCounts.NotReady))
	clusterGroupResourcesOrphaned.With(labels).Set(float64(status.ResourceCounts.Orphaned))
	clusterGroupResourcesReady.With(labels).Set(float64(status.ResourceCounts.Ready))
	clusterGroupResourcesUnknown.With(labels).Set(float64(status.ResourceCounts.Unknown))
	clusterGroupResourcesWaitApplied.With(labels).Set(float64(status.ResourceCounts.WaitApplied))
	clusterGroupDesiredReadyBundles.With(labels).Set(float64(status.Summary.DesiredReady))
	clusterGroupReadyBundles.With(labels).Set(float64(status.Summary.Ready))
	clusterGroupObserved.With(labels).Inc()

	for _, state := range clusterGroupStates {
		labels["state"] = state

		if state == status.Display.State {
			clusterGroupState.With(labels).Set(1)
		} else {
			clusterGroupState.With(labels).Set(0)
		}
	}
}
