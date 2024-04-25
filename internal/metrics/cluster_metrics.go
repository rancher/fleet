package metrics

import (
	"fmt"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	clusterSubsystem = "cluster"
	clusterLabels    = []string{
		"name",
		"namespace",
		// The name as given per "management.cattle.io/cluster-name" label. This
		// may but does not have to be different from `name` label and is added
		// by Rancher.
		"cluster_name",
		"cluster_display_name",
		"generation",
		"state",
	}

	clusterNameLabel        = "management.cattle.io/cluster-name"
	clusterDisplayNameLabel = "management.cattle.io/cluster-display-name"
	clusterStates           = []string{
		string(fleet.NotReady),
		string(fleet.Ready),
		"WaitCheckIn",
	}

	ClusterCollector = CollectorCollection{
		clusterSubsystem,
		clusterMetrics,
		collectClusterMetrics,
	}

	clusterMetrics = map[string]prometheus.Collector{
		"desired_ready_git_repos": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterSubsystem,
				Name:      "desired_ready_git_repos",
				Help:      "The desired number of GitRepos to be in a ready state.",
			},
			clusterLabels,
		),
		"ready_git_repos": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterSubsystem,
				Name:      "ready_git_repos",
				Help:      "The number of GitRepos in a ready state.",
			},
			clusterLabels,
		),
		"resources_count_desiredready": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterSubsystem,
				Name:      "resources_count_desiredready",
				Help:      "The number of resources for the given cluster desired to be in the Ready state.",
			},
			clusterLabels,
		),
		"resources_count_missing": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterSubsystem,
				Name:      "resources_count_missing",
				Help:      "The number of resources in the Missing state.",
			},
			clusterLabels,
		),
		"resources_count_modified": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterSubsystem,
				Name:      "resources_count_modified",
				Help:      "The number of resources in the Modified state.",
			},
			clusterLabels,
		),
		"resources_count_notready": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterSubsystem,
				Name:      "resources_count_notready",
				Help:      "The number of resources in the NotReady state.",
			},
			clusterLabels,
		),
		"resources_count_orphaned": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterSubsystem,
				Name:      "resources_count_orphaned",
				Help:      "The number of resources in the Orphaned state.",
			},
			clusterLabels,
		),
		"resources_count_ready": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterSubsystem,
				Name:      "resources_count_ready",
				Help:      "The number of resources in the Ready state.",
			},
			clusterLabels,
		),
		"resources_count_unknown": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterSubsystem,
				Name:      "resources_count_unknown",
				Help:      "The number of resources in the Unknown state.",
			},
			clusterLabels,
		),
		"resources_count_waitapplied": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterSubsystem,
				Name:      "resources_count_waitapplied",
				Help:      "The number of resources in the WaitApplied state.",
			},
			clusterLabels,
		),
		"state": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: clusterSubsystem,
				Name:      "state",
				Help:      "The current state of a given cluster",
			},
			clusterLabels,
		),
	}
)

func collectClusterMetrics(obj any, metrics map[string]prometheus.Collector) {
	cluster, ok := obj.(*fleet.Cluster)
	if !ok {
		panic("unexpected object type")
	}

	labels := prometheus.Labels{
		"name":                 cluster.Name,
		"namespace":            cluster.Namespace,
		"cluster_name":         cluster.ObjectMeta.Labels[clusterNameLabel],
		"cluster_display_name": cluster.ObjectMeta.Labels[clusterDisplayNameLabel],
		"generation":           fmt.Sprintf("%d", cluster.ObjectMeta.Generation),
		"state":                cluster.Status.Display.State,
	}

	metrics["desired_ready_git_repos"].(*prometheus.GaugeVec).
		With(labels).Set(float64(cluster.Status.DesiredReadyGitRepos))
	metrics["ready_git_repos"].(*prometheus.GaugeVec).
		With(labels).Set(float64(cluster.Status.ReadyGitRepos))
	metrics["resources_count_desiredready"].(*prometheus.GaugeVec).
		With(labels).Set(float64(cluster.Status.ResourceCounts.DesiredReady))
	metrics["resources_count_missing"].(*prometheus.GaugeVec).
		With(labels).Set(float64(cluster.Status.ResourceCounts.Missing))
	metrics["resources_count_modified"].(*prometheus.GaugeVec).
		With(labels).Set(float64(cluster.Status.ResourceCounts.Modified))
	metrics["resources_count_notready"].(*prometheus.GaugeVec).
		With(labels).Set(float64(cluster.Status.ResourceCounts.NotReady))
	metrics["resources_count_orphaned"].(*prometheus.GaugeVec).
		With(labels).Set(float64(cluster.Status.ResourceCounts.Orphaned))
	metrics["resources_count_ready"].(*prometheus.GaugeVec).
		With(labels).Set(float64(cluster.Status.ResourceCounts.Ready))
	metrics["resources_count_unknown"].(*prometheus.GaugeVec).
		With(labels).Set(float64(cluster.Status.ResourceCounts.Unknown))
	metrics["resources_count_waitapplied"].(*prometheus.GaugeVec).
		With(labels).Set(float64(cluster.Status.ResourceCounts.WaitApplied))

	for _, state := range clusterStates {
		labels["state"] = state

		if state == cluster.Status.Display.State {
			metrics["state"].(*prometheus.GaugeVec).With(labels).Set(1)
		} else {
			metrics["state"].(*prometheus.GaugeVec).With(labels).Set(0)
		}
	}
}
