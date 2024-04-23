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
	clusterGroupStates    = []string{
		string(fleet.NotReady),
		string(fleet.Ready),
	}
	ClusterGroupCollector = CollectorCollection{
		clusterGroupSubsystem,
		clusterGroupMetrics,
		collectClusterGroupMetrics,
	}
	clusterGroupMetrics = map[string]prometheus.Collector{
		"cluster_count": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterGroupSubsystem,
				Name:      "cluster_count",
				Help:      "The count of clusters in this cluster group.",
			},
			clusterGroupLabels,
		),
		"non_ready_cluster_count": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterGroupSubsystem,
				Name:      "non_ready_cluster_count",
				Help:      "The count of non ready clusters in this cluster group.",
			},
			clusterGroupLabels,
		),
		"resource_count_desired_ready": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterGroupSubsystem,
				Name:      "resource_count_desired_ready",
				Help:      "The count of resources that are desired to be in the Ready state.",
			},
			clusterGroupLabels,
		),
		"resource_count_missing": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterGroupSubsystem,
				Name:      "resource_count_missing",
				Help:      "The count of resources that are in a Missing state.",
			},
			clusterGroupLabels,
		),
		"resource_count_modified": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterGroupSubsystem,
				Name:      "resource_count_modified",
				Help:      "The count of resources that are in a Modified state.",
			},
			clusterGroupLabels,
		),
		"resource_count_notready": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterGroupSubsystem,
				Name:      "resource_count_notready",
				Help:      "The count of resources that are in a NotReady state.",
			},
			clusterGroupLabels,
		),
		"resource_count_orphaned": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterGroupSubsystem,
				Name:      "resource_count_orphaned",
				Help:      "The count of resources that are in an Orphaned state.",
			},
			clusterGroupLabels,
		),
		"resource_count_ready": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterGroupSubsystem,
				Name:      "resource_count_ready",
				Help:      "The count of resources that are in a Ready state.",
			},
			clusterGroupLabels,
		),
		"resource_count_unknown": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterGroupSubsystem,
				Name:      "resource_count_unknown",
				Help:      "The count of resources that are in an Unknown state.",
			},
			clusterGroupLabels,
		),
		"resource_count_waitapplied": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterGroupSubsystem,
				Name:      "resource_count_waitapplied",
				Help:      "The count of resources that are in a WaitApplied state.",
			},
			clusterGroupLabels,
		),
		"bundle_desired_ready": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterGroupSubsystem,
				Name:      "bundle_desired_ready",
				Help:      "The count of bundles that are desired to be in a Ready state.",
			},
			clusterGroupLabels,
		),
		"bundle_ready": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterGroupSubsystem,
				Name:      "bundle_ready",
				Help:      "The count of bundles that are in a Ready state in the Cluster Group.",
			},
			clusterGroupLabels,
		),
		"state": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterGroupSubsystem,
				Name:      "state",
				Help:      "The current state of a given cluster group.",
			},
			clusterGroupLabels,
		),
	}
)

func collectClusterGroupMetrics(obj any, metrics map[string]prometheus.Collector) {
	clusterGroup, ok := obj.(*fleet.ClusterGroup)
	if !ok {
		panic("unexpected object type")
	}

	labels := prometheus.Labels{
		"name":       clusterGroup.Name,
		"namespace":  clusterGroup.Namespace,
		"generation": fmt.Sprintf("%d", clusterGroup.ObjectMeta.Generation),
		"state":      clusterGroup.Status.Display.State,
	}

	metrics["cluster_count"].(*prometheus.GaugeVec).With(labels).
		Set(float64(clusterGroup.Status.ClusterCount))
	metrics["non_ready_cluster_count"].(*prometheus.GaugeVec).With(labels).
		Set(float64(clusterGroup.Status.NonReadyClusterCount))
	metrics["resource_count_desired_ready"].(*prometheus.GaugeVec).With(labels).
		Set(float64(clusterGroup.Status.ResourceCounts.DesiredReady))
	metrics["resource_count_missing"].(*prometheus.GaugeVec).With(labels).
		Set(float64(clusterGroup.Status.ResourceCounts.Missing))
	metrics["resource_count_modified"].(*prometheus.GaugeVec).With(labels).
		Set(float64(clusterGroup.Status.ResourceCounts.Modified))
	metrics["resource_count_notready"].(*prometheus.GaugeVec).With(labels).
		Set(float64(clusterGroup.Status.ResourceCounts.NotReady))
	metrics["resource_count_orphaned"].(*prometheus.GaugeVec).With(labels).
		Set(float64(clusterGroup.Status.ResourceCounts.Orphaned))
	metrics["resource_count_ready"].(*prometheus.GaugeVec).With(labels).
		Set(float64(clusterGroup.Status.ResourceCounts.Ready))
	metrics["resource_count_unknown"].(*prometheus.GaugeVec).With(labels).
		Set(float64(clusterGroup.Status.ResourceCounts.Unknown))
	metrics["resource_count_waitapplied"].(*prometheus.GaugeVec).With(labels).
		Set(float64(clusterGroup.Status.ResourceCounts.WaitApplied))
	metrics["bundle_desired_ready"].(*prometheus.GaugeVec).With(labels).
		Set(float64(clusterGroup.Status.Summary.DesiredReady))
	metrics["bundle_ready"].(*prometheus.GaugeVec).With(labels).
		Set(float64(clusterGroup.Status.Summary.Ready))

	for _, state := range clusterGroupStates {
		labels["state"] = state

		if state == clusterGroup.Status.Display.State {
			metrics["state"].(*prometheus.GaugeVec).With(labels).Set(1)
		} else {
			metrics["state"].(*prometheus.GaugeVec).With(labels).Set(0)
		}
	}
}
