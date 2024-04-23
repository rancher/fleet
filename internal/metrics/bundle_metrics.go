package metrics

import (
	"fmt"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	bundleSubsystem = "bundle"
	bundleLabels    = []string{"name", "namespace", "commit", "repo", "generation", "state"}
	BundleCollector = CollectorCollection{
		subsystem: bundleSubsystem,
		metrics: map[string]prometheus.Collector{
			"not_ready": promauto.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: metricPrefix,
					Subsystem: bundleSubsystem,
					Name:      "not_ready",
					Help:      "Number of deployments for a specific bundle in a not ready state.",
				},
				bundleLabels,
			),
			"wait_applied": promauto.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: metricPrefix,
					Subsystem: bundleSubsystem,
					Name:      "wait_applied",
					Help:      "Number of deployments for a specific bundle in a wait applied state.",
				},
				bundleLabels,
			),
			"err_applied": promauto.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: metricPrefix,
					Subsystem: bundleSubsystem,
					Name:      "err_applied",
					Help:      "Number of deployments for a specific bundle in an error applied state.",
				},
				bundleLabels,
			),
			"out_of_sync": promauto.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: metricPrefix,
					Subsystem: bundleSubsystem,
					Name:      "out_of_sync",
					Help:      "Number of deployments for a specific bundle in an out of sync state.",
				},
				bundleLabels,
			),
			"modified": promauto.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: metricPrefix,
					Subsystem: bundleSubsystem,
					Name:      "modified",
					Help:      "Number of deployments for a specific bundle in a modified state.",
				},
				bundleLabels,
			),
			"ready": promauto.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: metricPrefix,
					Subsystem: bundleSubsystem,
					Name:      "ready",
					Help:      "Number of deployments for a specific bundle in a ready state.",
				},
				bundleLabels,
			),
			"pending": promauto.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: metricPrefix,
					Subsystem: bundleSubsystem,
					Name:      "pending",
					Help:      "Number of deployments for a specific bundle in a pending state.",
				},
				bundleLabels,
			),
			"desired_ready": promauto.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: metricPrefix,
					Subsystem: bundleSubsystem,
					Name:      "desired_ready",
					Help:      "Number of deployments that are desired to be ready for a bundle.",
				},
				bundleLabels,
			),
			"state": promauto.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: metricPrefix,
					Subsystem: bundleSubsystem,
					Name:      "state",
					Help:      "Shows the state of this bundle based on the state label. A value of 1 is true 0, is false.",
				},
				bundleLabels,
			),
		},
		collector: collectBundleMetrics,
	}
)

func collectBundleMetrics(obj any, metrics map[string]prometheus.Collector) {
	bundle, ok := obj.(*fleet.Bundle)
	if !ok {
		panic("unexpected object type")
	}

	currentState := summary.GetSummaryState(bundle.Status.Summary)
	labels := prometheus.Labels{
		"name":       bundle.Name,
		"namespace":  bundle.Namespace,
		"commit":     bundle.ObjectMeta.Labels[commitLabel],
		"repo":       bundle.ObjectMeta.Labels[repoNameLabel],
		"generation": fmt.Sprintf("%d", bundle.ObjectMeta.Generation),
		"state":      string(currentState),
	}

	metrics["not_ready"].(*prometheus.GaugeVec).With(labels).
		Set(float64(bundle.Status.Summary.NotReady))
	metrics["wait_applied"].(*prometheus.GaugeVec).With(labels).
		Set(float64(bundle.Status.Summary.WaitApplied))
	metrics["err_applied"].(*prometheus.GaugeVec).With(labels).
		Set(float64(bundle.Status.Summary.ErrApplied))
	metrics["out_of_sync"].(*prometheus.GaugeVec).With(labels).
		Set(float64(bundle.Status.Summary.OutOfSync))
	metrics["modified"].(*prometheus.GaugeVec).With(labels).
		Set(float64(bundle.Status.Summary.Modified))
	metrics["ready"].(*prometheus.GaugeVec).With(labels).
		Set(float64(bundle.Status.Summary.Ready))
	metrics["pending"].(*prometheus.GaugeVec).With(labels).
		Set(float64(bundle.Status.Summary.Pending))
	metrics["desired_ready"].(*prometheus.GaugeVec).With(labels).
		Set(float64(bundle.Status.Summary.DesiredReady))

	for _, state := range bundleStates {
		labels["state"] = string(state)

		if state == currentState {
			metrics["state"].(*prometheus.GaugeVec).With(labels).Set(1)
		} else {
			metrics["state"].(*prometheus.GaugeVec).With(labels).Set(0)
		}
	}
}
