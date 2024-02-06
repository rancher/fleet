package metrics

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
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
	gitRepoMetrics = map[string]prometheus.Collector{
		"resources_desired_ready": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: gitRepoSubsystem,
				Name:      "resources_desired_ready",
				Help:      "The count of resources that are desired to be in a Ready state.",
			},
			gitRepoLabels,
		),
		"resources_missing": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: gitRepoSubsystem,
				Name:      "resources_missing",
				Help:      "The count of resources that are in a Missing state.",
			},
			gitRepoLabels,
		),
		"resources_modified": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: gitRepoSubsystem,
				Name:      "resources_modified",
				Help:      "The count of resources that are in a Modified state.",
			},
			gitRepoLabels,
		),
		"resources_not_ready": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: gitRepoSubsystem,
				Name:      "resources_not_ready",
				Help:      "The count of resources that are in a NotReady state.",
			},
			gitRepoLabels,
		),
		"resources_orphaned": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: gitRepoSubsystem,
				Name:      "resources_orphaned",
				Help:      "The count of resources that are in an Orphaned state.",
			},
			gitRepoLabels,
		),
		"resources_ready": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: gitRepoSubsystem,
				Name:      "resources_ready",
				Help:      "The count of resources that are in a Ready state.",
			},
			gitRepoLabels,
		),
		"resources_unknown": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: gitRepoSubsystem,
				Name:      "resources_unknown",
				Help:      "The count of resources that are in an Unknown state.",
			},
			gitRepoLabels,
		),
		"resources_wait_applied": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: gitRepoSubsystem,
				Name:      "resources_wait_applied",
				Help:      "The count of resources that are in a WaitApplied state.",
			},
			gitRepoLabels,
		),
		"desired_ready_clusters": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: gitRepoSubsystem,
				Name:      "desired_ready_clusters",
				Help:      "The amount of clusters desired to be in a ready state.",
			},
			gitRepoLabels,
		),
		"ready_clusters": promauto.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: metricPrefix,
				Subsystem: gitRepoSubsystem,
				Name:      "ready_clusters",
				Help:      "The count of clusters in a Ready state.",
			},
			gitRepoLabels,
		),
	}
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
