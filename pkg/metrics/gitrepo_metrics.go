package metrics

import (
	"strings"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	gitRepoSubsystem = "gitrepo"
	gitRepoLabels    = []string{"name", "namespace", "repo", "branch", "paths"}

	gitrepoResourcesDesiredReady = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: gitRepoSubsystem,
			Name:      "resources_desired_ready",
			Help:      "The count of resources that are desired to be in a Ready state.",
		},
		gitRepoLabels,
	)
	gitrepoResourcesMissing = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: gitRepoSubsystem,
			Name:      "resources_missing",
			Help:      "The count of resources that are in a Missing state.",
		},
		gitRepoLabels,
	)
	gitrepoResourcesModified = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: gitRepoSubsystem,
			Name:      "resources_modified",
			Help:      "The count of resources that are in a Modified state.",
		},
		gitRepoLabels,
	)
	gitrepoResourcesNotReady = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: gitRepoSubsystem,
			Name:      "resources_not_ready",
			Help:      "The count of resources that are in a NotReady state.",
		},
		gitRepoLabels,
	)
	gitrepoResourcesOrphaned = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: gitRepoSubsystem,
			Name:      "resources_orphaned",
			Help:      "The count of resources that are in an Orphaned state.",
		},
		gitRepoLabels,
	)
	gitrepoResourcesReady = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: gitRepoSubsystem,
			Name:      "resources_ready",
			Help:      "The count of resources that are in a Ready state.",
		},
		gitRepoLabels,
	)
	gitrepoResourcesUnknown = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: gitRepoSubsystem,
			Name:      "resources_unknown",
			Help:      "The count of resources that are in an Unknown state.",
		},
		gitRepoLabels,
	)
	gitrepoResourcesWaitApplied = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: gitRepoSubsystem,
			Name:      "resources_wait_applied",
			Help:      "The count of resources that are in a WaitApplied state.",
		},
		gitRepoLabels,
	)
	gitrepoDesiredReadyClusters = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: gitRepoSubsystem,
			Name:      "desired_ready_clusters",
			Help:      "The amount of clusters desired to be in a ready state.",
		},
		gitRepoLabels,
	)
	gitrepoReadyClusters = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: gitRepoSubsystem,
			Name:      "ready_clusters",
			Help:      "The count of cluster in a Ready state.",
		},
		gitRepoLabels,
	)
	gitrepoObserved = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: gitRepoSubsystem,
			Name:      "observations_total",
			Help:      "The total times that this GitRepo has been observed",
		},
		gitRepoLabels,
	)
)

func CollectGitRepoMetrics(gitrepo *fleet.GitRepo, status *fleet.GitRepoStatus) {
	labels := prometheus.Labels{
		"name":      gitrepo.Name,
		"namespace": gitrepo.Namespace,
		"repo":      gitrepo.Spec.Repo,
		"branch":    gitrepo.Spec.Branch,
		"paths":     strings.Join(gitrepo.Spec.Paths, ";"),
	}

	gitrepoDesiredReadyClusters.With(labels).Set(float64(status.DesiredReadyClusters))
	gitrepoReadyClusters.With(labels).Set(float64(status.ReadyClusters))
	gitrepoResourcesMissing.With(labels).Set(float64(status.ResourceCounts.Missing))
	gitrepoResourcesModified.With(labels).Set(float64(status.ResourceCounts.Modified))
	gitrepoResourcesNotReady.With(labels).Set(float64(status.ResourceCounts.NotReady))
	gitrepoResourcesOrphaned.With(labels).Set(float64(status.ResourceCounts.Orphaned))
	gitrepoResourcesReady.With(labels).Set(float64(status.ResourceCounts.Ready))
	gitrepoResourcesUnknown.With(labels).Set(float64(status.ResourceCounts.Unknown))
	gitrepoResourcesWaitApplied.With(labels).Set(float64(status.ResourceCounts.WaitApplied))
	gitrepoObserved.With(labels).Inc()
}
