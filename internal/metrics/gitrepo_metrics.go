package metrics

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var (
	gitRepoSubsystem = "gitrepo"
	gitRepoLabels    = []string{"name", "namespace", "repo", "branch", "paths"}
	GitRepoCollector = CollectorCollection{
		gitRepoSubsystem,
		gitRepoMetrics,
		collectGitRepoMetrics,
	}
	gitRepoMetrics        = getStatusMetrics(gitRepoSubsystem, gitRepoLabels)
	collectGitRepoMetrics = func(
		obj any,
		metrics map[string]prometheus.Collector,
	) {
		gitrepo, ok := obj.(*fleet.GitRepo)
		if !ok {
			panic("unexpected object type")
		}

		labels := prometheus.Labels{
			"name":      gitrepo.Name,
			"namespace": gitrepo.Namespace,
			"repo":      gitrepo.Spec.Repo,
			"branch":    gitrepo.Spec.Branch,
			"paths":     strings.Join(gitrepo.Spec.Paths, ";"),
		}

		metrics["desired_ready_clusters"].(*prometheus.GaugeVec).
			With(labels).Set(float64(gitrepo.Status.DesiredReadyClusters))
		metrics["ready_clusters"].(*prometheus.GaugeVec).
			With(labels).Set(float64(gitrepo.Status.ReadyClusters))
		metrics["resources_missing"].(*prometheus.GaugeVec).
			With(labels).Set(float64(gitrepo.Status.ResourceCounts.Missing))
		metrics["resources_modified"].(*prometheus.GaugeVec).
			With(labels).Set(float64(gitrepo.Status.ResourceCounts.Modified))
		metrics["resources_not_ready"].(*prometheus.GaugeVec).
			With(labels).Set(float64(gitrepo.Status.ResourceCounts.NotReady))
		metrics["resources_orphaned"].(*prometheus.GaugeVec).
			With(labels).Set(float64(gitrepo.Status.ResourceCounts.Orphaned))
		metrics["resources_desired_ready"].(*prometheus.GaugeVec).
			With(labels).Set(float64(gitrepo.Status.ResourceCounts.DesiredReady))
		metrics["resources_ready"].(*prometheus.GaugeVec).
			With(labels).Set(float64(gitrepo.Status.ResourceCounts.Ready))
		metrics["resources_unknown"].(*prometheus.GaugeVec).
			With(labels).Set(float64(gitrepo.Status.ResourceCounts.Unknown))
		metrics["resources_wait_applied"].(*prometheus.GaugeVec).
			With(labels).Set(float64(gitrepo.Status.ResourceCounts.WaitApplied))
	}
)
