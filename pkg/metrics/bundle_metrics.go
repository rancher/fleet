package metrics

import (
	"fmt"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/summary"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	bundleSubsystem = "bundle"
	bundleLabels    = []string{"name", "namespace", "commit", "repo", "generation", "state"}

	bundleNotReadyDeployments = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundleSubsystem,
			Name:      "not_ready",
			Help:      "Number of deployments for a specific bundle in a not ready state.",
		},
		bundleLabels,
	)
	bundleWaitAppliedDeployments = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundleSubsystem,
			Name:      "wait_applied",
			Help:      "Number of deployments for a specific bundle in a wait applied state.",
		},
		bundleLabels,
	)
	bundleErrAppliedDeployments = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundleSubsystem,
			Name:      "err_applied",
			Help:      "Number of deployments for a specific bundle in a error applied state.",
		},
		bundleLabels,
	)
	bundleOutOfSyncDeployments = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundleSubsystem,
			Name:      "out_of_sync",
			Help:      "Number of deployments for a specific bundle in a out of sync state.",
		},
		bundleLabels,
	)
	bundleModifiedDeployments = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundleSubsystem,
			Name:      "modified",
			Help:      "Number of deployments for a specific bundle in a modified state.",
		},
		bundleLabels,
	)
	bundleReadyDeployments = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundleSubsystem,
			Name:      "ready",
			Help:      "Number of deployments for a specific bundle in a ready state.",
		},
		bundleLabels,
	)
	bundlePendingDeployments = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundleSubsystem,
			Name:      "pending",
			Help:      "Number of deployments for a specific bundle in a pending state.",
		},
		bundleLabels,
	)
	bundleDesiredReadyDeployments = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundleSubsystem,
			Name:      "desired_ready",
			Help:      "Number of deployments that are desired to be ready for a bundle.",
		},
		bundleLabels,
	)
	bundleObserved = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: bundleSubsystem,
			Name:      "observations_total",
			Help:      "The total times that this bundle has been observed",
		},
		bundleLabels,
	)
	bundleState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundleSubsystem,
			Name:      "state",
			Help:      "Shows the state of this bundle based on the state label. A value of 1 is true 0 is false.",
		},
		bundleLabels,
	)
)

func CollectBundleMetrics(bundle *fleet.Bundle, status *fleet.BundleStatus) {
	labels := prometheus.Labels{
		"name":       bundle.Name,
		"namespace":  bundle.Namespace,
		"commit":     bundle.ObjectMeta.Labels["fleet.cattle.io/commit"],
		"repo":       bundle.ObjectMeta.Labels["fleet.cattle.io/repo-name"],
		"generation": fmt.Sprintf("%d", bundle.ObjectMeta.Generation),
		"state":      string(summary.GetSummaryState(bundle.Status.Summary)),
	}

	bundleNotReadyDeployments.With(labels).Set(float64(status.Summary.NotReady))
	bundleWaitAppliedDeployments.With(labels).Set(float64(status.Summary.WaitApplied))
	bundleErrAppliedDeployments.With(labels).Set(float64(status.Summary.ErrApplied))
	bundleOutOfSyncDeployments.With(labels).Set(float64(status.Summary.OutOfSync))
	bundleModifiedDeployments.With(labels).Set(float64(status.Summary.Modified))
	bundleReadyDeployments.With(labels).Set(float64(status.Summary.Ready))
	bundlePendingDeployments.With(labels).Set(float64(status.Summary.Pending))
	bundleDesiredReadyDeployments.With(labels).Set(float64(status.Summary.DesiredReady))
	bundleObserved.With(labels).Inc()

	currentState := summary.GetSummaryState(bundle.Status.Summary)

	for _, state := range bundleStates {
		labels["state"] = string(state)

		if state == currentState {
			bundleState.With(labels).Set(1)
		} else {
			bundleState.With(labels).Set(0)
		}
	}
}
