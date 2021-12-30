package metrics

import (
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/summary"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	bundle_subsystem = "bundle"
	bundle_labels    = []string{"name", "namespace"}

	bundleNotReadyDeployments = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundle_subsystem,
			Name:      "not_ready",
			Help:      "Number of deployments for a specific bundle in a not ready state.",
		},
		bundle_labels,
	)
	bundleWaitAppliedDeployments = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundle_subsystem,
			Name:      "wait_applied",
			Help:      "Number of deployments for a specific bundle in a wait applied state.",
		},
		bundle_labels,
	)
	bundleErrAppliedDeployments = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundle_subsystem,
			Name:      "err_applied",
			Help:      "Number of deployments for a specific bundle in a error applied state.",
		},
		bundle_labels,
	)
	bundleOutOfSyncDeployments = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundle_subsystem,
			Name:      "out_of_sync",
			Help:      "Number of deployments for a specific bundle in a out of sync state.",
		},
		bundle_labels,
	)
	bundleModifiedDeployments = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundle_subsystem,
			Name:      "modified",
			Help:      "Number of deployments for a specific bundle in a modified state.",
		},
		bundle_labels,
	)
	bundleReadyDeployments = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundle_subsystem,
			Name:      "ready",
			Help:      "Number of deployments for a specific bundle in a ready state.",
		},
		bundle_labels,
	)
	bundlePendingDeployments = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundle_subsystem,
			Name:      "pending",
			Help:      "Number of deployments for a specific bundle in a pending state.",
		},
		bundle_labels,
	)
	bundleDesiredReadyDeployments = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundle_subsystem,
			Name:      "desired_ready",
			Help:      "Number of deployments that are desired to be ready for a bundle.",
		},
		bundle_labels,
	)
	bundleObserved = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: bundle_subsystem,
			Name:      "total_observations",
			Help:      "The total times that this bundle has been observed",
		},
		bundle_labels,
	)
	bundleState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundle_subsystem,
			Name:      "state",
			Help:      "Shows the state of this bundle deployment. Ready = 1, NotReady = 2, Pending = 3, OutOfSync = 4, Modified = 5, WaitApplied = 6, ErrApplied = 7.",
		},
		bundle_labels,
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
