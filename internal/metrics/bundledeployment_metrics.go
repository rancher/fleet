package metrics

import (
	"fmt"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	bundledeploymentSubsystem = "bundledeployment"
	bundledeploymentLabels    = []string{
		"name",
		"namespace",
		"cluster_name",
		"cluster_namespace",
		"repo",
		"commit",
		"bundle",
		"bundle_namespace",
		"generation",
		"state",
	}

	bundleDeploymentState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundledeploymentSubsystem,
			Name:      "state",
			Help: "Shows the state of this bundle deployment based on the state label. " +
				"A value of 1 is true 0 is false.",
		},
		bundledeploymentLabels,
	)

	bundleDeploymentObserved = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: bundledeploymentSubsystem,
			Name:      "observations_total",
			Help:      "The total times that this bundle deployment has been observed",
		},
		bundledeploymentLabels,
	)
)

func CollectBundleDeploymentMetrics(bundleDep *fleet.BundleDeployment) {
	if !enabled {
		return
	}

	labels := prometheus.Labels{
		"name":              bundleDep.Name,
		"namespace":         bundleDep.Namespace,
		"cluster_name":      bundleDep.ObjectMeta.Labels["fleet.cattle.io/cluster"],
		"cluster_namespace": bundleDep.ObjectMeta.Labels["fleet.cattle.io/cluster-namespace"],
		"repo":              bundleDep.ObjectMeta.Labels[repoNameLabel],
		"commit":            bundleDep.ObjectMeta.Labels[commitLabel],
		"bundle":            bundleDep.ObjectMeta.Labels["fleet.cattle.io/bundle-name"],
		"bundle_namespace":  bundleDep.ObjectMeta.Labels["fleet.cattle.io/bundle-namespace"],
		"generation":        fmt.Sprintf("%d", bundleDep.ObjectMeta.Generation),
		"state":             string(summary.GetDeploymentState(bundleDep)),
	}
	bundleDeploymentObserved.With(labels).Inc()

	currentState := summary.GetDeploymentState(bundleDep)

	for _, state := range bundleStates {
		labels["state"] = string(state)

		if state == currentState {
			bundleDeploymentState.With(labels).Set(1)
		} else {
			bundleDeploymentState.With(labels).Set(0)
		}
	}
}

func registerBundleDeploymentMetrics() {
	metrics.Registry.MustRegister(bundleDeploymentState)
	metrics.Registry.MustRegister(bundleDeploymentObserved)
}
