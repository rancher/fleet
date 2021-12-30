package metrics

import (
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/summary"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	bundleSubsystem = "bundle"
	bundleLabels    = []string{"name", "namespace"}

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
			Name:      "total_observations",
			Help:      "The total times that this bundle has been observed",
		},
		bundleLabels,
	)
	bundleState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundleSubsystem,
			Name:      "state",
			Help:      "Shows the state of this bundle deployment. Ready = 1, NotReady = 2, Pending = 3, OutOfSync = 4, Modified = 5, WaitApplied = 6, ErrApplied = 7.",
		},
		bundleLabels,
	)
)

func ObserveBundle(bundle *fleet.Bundle, status *fleet.BundleStatus) {
	labels := prometheus.Labels{
		"name":      bundle.Name,
		"namespace": bundle.Namespace,
	}

	bundleNotReadyDeployments.With(labels).Set(float64(status.Summary.NotReady))
	bundleWaitAppliedDeployments.With(labels).Set(float64(status.Summary.WaitApplied))
	bundleErrAppliedDeployments.With(labels).Set(float64(status.Summary.ErrApplied))
	bundleOutOfSyncDeployments.With(labels).Set(float64(status.Summary.OutOfSync))
	bundleModifiedDeployments.With(labels).Set(float64(status.Summary.Modified))
	bundleReadyDeployments.With(labels).Set(float64(status.Summary.Ready))
	bundlePendingDeployments.With(labels).Set(float64(status.Summary.Pending))
	bundleDesiredReadyDeployments.With(labels).Set(float64(status.Summary.DesiredReady))
	bundleState.With(labels).Set(float64(fleet.StateRank[summary.GetSummaryState(bundle.Status.Summary)]))
	bundleObserved.With(labels).Inc()
}
