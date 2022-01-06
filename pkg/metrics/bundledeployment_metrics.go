package metrics

import (
	"fmt"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/summary"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	bundledeploymentSubsystem = "bundledeployment"
	bundledeploymentLabels    = []string{"name", "namespace", "cluster_name", "cluster_display_name", "repo", "commit", "bundle", "bundle_namespace", "generation", "state"}

	bundleDeploymentState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundledeploymentSubsystem,
			Name:      "state",
			Help:      "Shows the state of this bundle deployment based on the state label. A value of 1 is true 0 is false.",
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

func CollectBundleDeploymentMetrics(bundleDep *fleet.BundleDeployment, status *fleet.BundleDeploymentStatus) {
	clusterName, clusterDisplayName := getClusterName(bundleDep)

	labels := prometheus.Labels{
		"name":                 bundleDep.Name,
		"namespace":            bundleDep.Namespace,
		"cluster_name":         clusterName,
		"cluster_display_name": clusterDisplayName,
		"repo":                 bundleDep.ObjectMeta.Labels["fleet.cattle.io/repo-name"],
		"commit":               bundleDep.ObjectMeta.Labels["fleet.cattle.io/commit"],
		"bundle":               bundleDep.ObjectMeta.Labels["fleet.cattle.io/bundle-name"],
		"bundle_namespace":     bundleDep.ObjectMeta.Labels["fleet.cattle.io/bundle-namespace"],
		"generation":           fmt.Sprintf("%d", bundleDep.ObjectMeta.Generation),
		"state":                string(summary.GetDeploymentState(bundleDep)),
	}
	bundleDeploymentObserved.With(labels).Inc()

	currentState := summary.GetDeploymentState(bundleDep)

	for _, state := range states {
		labels["state"] = string(state)

		if state == currentState {
			bundleDeploymentState.With(labels).Set(1)
		} else {
			bundleDeploymentState.With(labels).Set(0)
		}
	}
}

func getClusterName(bundleDep *fleet.BundleDeployment) (string, string) {
	name, ok := bundleDep.Spec.StagedOptions.Helm.Values.Global.Fleet.ClusterLabels[clusterNameLabel]
	if !ok {
		name = ""
	}

	displayName, ok := bundleDep.Spec.StagedOptions.Helm.Values.Global.Fleet.ClusterLabels[clusterDisplayNameLabel]
	if !ok {
		displayName = ""
	}

	return name, displayName
}
