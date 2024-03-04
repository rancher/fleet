package metrics

import (
	"fmt"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	clusterSubsystem = "cluster"
	clusterLabels    = []string{
		"name",
		"namespace",
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

	clusterAgentNodesReady = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterSubsystem,
			Name:      "agent_nodes_ready",
			Help:      "The number of fleet agents in a Ready status for a given cluster.",
		},
		clusterLabels,
	)
	clusterAgentNodesNotReady = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterSubsystem,
			Name:      "agent_nodes_not_ready",
			Help:      "The number of fleet agents not in a Ready status for a given cluster.",
		},
		clusterLabels,
	)
	clusterDesiredReadyGitRepos = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterSubsystem,
			Name:      "desired_ready_git_repos",
			Help:      "The desired number of GitRepos to be in a ready state.",
		},
		clusterLabels,
	)
	clusterReadyGitRepos = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterSubsystem,
			Name:      "ready_git_repos",
			Help:      "The number of GitRepos in a ready state.",
		},
		clusterLabels,
	)
	clusterResourcesDesiredReady = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterSubsystem,
			Name:      "resources_count_desiredready",
			Help:      "The number of resources for the given cluster desired to be in the Ready state.",
		},
		clusterLabels,
	)
	clusterResourcesMissing = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterSubsystem,
			Name:      "resources_count_missing",
			Help:      "The number of resources in the Missing state.",
		},
		clusterLabels,
	)
	clusterResourcesModified = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterSubsystem,
			Name:      "resources_count_modified",
			Help:      "The number of resources in the Modified state.",
		},
		clusterLabels,
	)
	clusterResourcesNotReady = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterSubsystem,
			Name:      "resources_count_notready",
			Help:      "The number of resources in the NotReady state.",
		},
		clusterLabels,
	)
	clusterResourcesOrphaned = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterSubsystem,
			Name:      "resources_count_orphaned",
			Help:      "The number of resources in the Orphaned state.",
		},
		clusterLabels,
	)
	clusterResourcesReady = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterSubsystem,
			Name:      "resources_count_ready",
			Help:      "The number of resources in the Ready state.",
		},
		clusterLabels,
	)
	clusterResourcesUnknown = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterSubsystem,
			Name:      "resources_count_unknown",
			Help:      "The number of resources in the Unknown state.",
		},
		clusterLabels,
	)
	clusterResourcesWaitApplied = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterSubsystem,
			Name:      "resources_count_waitapplied",
			Help:      "The number of resources in the WaitApplied state.",
		},
		clusterLabels,
	)
	clusterState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: clusterSubsystem,
			Name:      "state",
			Help:      "The current state of a given cluster",
		},
		clusterLabels,
	)
	clusterObserved = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: clusterSubsystem,
			Name:      "observations_total",
			Help:      "The total times that this cluster has been observed",
		},
		clusterLabels,
	)
)

func CollectClusterMetrics(cluster *fleet.Cluster) {
	if !enabled {
		return
	}

	labels := prometheus.Labels{
		"name":                 cluster.Name,
		"namespace":            cluster.Namespace,
		"cluster_name":         cluster.ObjectMeta.Labels[clusterNameLabel],
		"cluster_display_name": cluster.ObjectMeta.Labels[clusterDisplayNameLabel],
		"generation":           fmt.Sprintf("%d", cluster.ObjectMeta.Generation),
		"state":                cluster.Status.Display.State,
	}

	clusterAgentNodesReady.With(labels).Set(float64(cluster.Status.Agent.ReadyNodes))
	clusterAgentNodesNotReady.With(labels).Set(float64(cluster.Status.Agent.NonReadyNodes))
	clusterDesiredReadyGitRepos.With(labels).Set(float64(cluster.Status.DesiredReadyGitRepos))
	clusterReadyGitRepos.With(labels).Set(float64(cluster.Status.ReadyGitRepos))
	clusterResourcesDesiredReady.With(labels).Set(float64(cluster.Status.ResourceCounts.DesiredReady))
	clusterResourcesMissing.With(labels).Set(float64(cluster.Status.ResourceCounts.Missing))
	clusterResourcesModified.With(labels).Set(float64(cluster.Status.ResourceCounts.Modified))
	clusterResourcesNotReady.With(labels).Set(float64(cluster.Status.ResourceCounts.NotReady))
	clusterResourcesOrphaned.With(labels).Set(float64(cluster.Status.ResourceCounts.Orphaned))
	clusterResourcesReady.With(labels).Set(float64(cluster.Status.ResourceCounts.Ready))
	clusterResourcesUnknown.With(labels).Set(float64(cluster.Status.ResourceCounts.Unknown))
	clusterResourcesWaitApplied.With(labels).Set(float64(cluster.Status.ResourceCounts.WaitApplied))
	clusterObserved.With(labels).Inc()

	for _, state := range clusterStates {
		labels["state"] = state

		if state == cluster.Status.Display.State {
			clusterState.With(labels).Set(1)
		} else {
			clusterState.With(labels).Set(0)
		}
	}
}

func registerClusterMetrics() {
	metrics.Registry.MustRegister(clusterAgentNodesReady)
	metrics.Registry.MustRegister(clusterAgentNodesNotReady)
	metrics.Registry.MustRegister(clusterDesiredReadyGitRepos)
	metrics.Registry.MustRegister(clusterReadyGitRepos)
	metrics.Registry.MustRegister(clusterResourcesDesiredReady)
	metrics.Registry.MustRegister(clusterResourcesMissing)
	metrics.Registry.MustRegister(clusterResourcesModified)
	metrics.Registry.MustRegister(clusterResourcesNotReady)
	metrics.Registry.MustRegister(clusterResourcesOrphaned)
	metrics.Registry.MustRegister(clusterResourcesReady)
	metrics.Registry.MustRegister(clusterResourcesUnknown)
	metrics.Registry.MustRegister(clusterResourcesWaitApplied)
	metrics.Registry.MustRegister(clusterObserved)
}
