package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var (
	helmSubsystem = "helmop"
	helmLabels    = []string{"name", "namespace", "repo", "chart", "version"}
	HelmCollector = CollectorCollection{
		helmSubsystem,
		helmMetrics,
		collectHelmMetrics,
	}
	helmMetrics        = getStatusMetrics(helmSubsystem, helmLabels)
	collectHelmMetrics = func(
		obj any,
		metrics map[string]prometheus.Collector,
	) {
		helm, ok := obj.(*fleet.HelmOp)
		if !ok {
			panic("unexpected object type")
		}

		labels := prometheus.Labels{
			"name":      helm.Name,
			"namespace": helm.Namespace,
			"repo":      helm.Spec.Helm.Repo,
			"chart":     helm.Spec.Helm.Chart,
			"version":   helm.Status.Version,
		}

		metrics["desired_ready_clusters"].(*prometheus.GaugeVec).
			With(labels).Set(float64(helm.Status.DesiredReadyClusters))
		metrics["ready_clusters"].(*prometheus.GaugeVec).
			With(labels).Set(float64(helm.Status.ReadyClusters))
		metrics["resources_missing"].(*prometheus.GaugeVec).
			With(labels).Set(float64(helm.Status.ResourceCounts.Missing))
		metrics["resources_modified"].(*prometheus.GaugeVec).
			With(labels).Set(float64(helm.Status.ResourceCounts.Modified))
		metrics["resources_not_ready"].(*prometheus.GaugeVec).
			With(labels).Set(float64(helm.Status.ResourceCounts.NotReady))
		metrics["resources_orphaned"].(*prometheus.GaugeVec).
			With(labels).Set(float64(helm.Status.ResourceCounts.Orphaned))
		metrics["resources_desired_ready"].(*prometheus.GaugeVec).
			With(labels).Set(float64(helm.Status.ResourceCounts.DesiredReady))
		metrics["resources_ready"].(*prometheus.GaugeVec).
			With(labels).Set(float64(helm.Status.ResourceCounts.Ready))
		metrics["resources_unknown"].(*prometheus.GaugeVec).
			With(labels).Set(float64(helm.Status.ResourceCounts.Unknown))
		metrics["resources_wait_applied"].(*prometheus.GaugeVec).
			With(labels).Set(float64(helm.Status.ResourceCounts.WaitApplied))
	}
)
