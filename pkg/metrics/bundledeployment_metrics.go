package metrics

import (
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/summary"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	bundledeploymentSubsystem = "bundledeployment"
	bundledeploymentLabels    = []string{"name", "namespace", "cluster_name", "cluster_display_name", "repo", "commit", "bundle", "bundle_namespace", "generation"}

	bundleDeploymentState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: bundledeploymentSubsystem,
			Name:      "state",
			Help:      "Shows the state of this bundle deployment. Ready = 1, NotReady = 2, Pending = 3, OutOfSync = 4, Modified = 5, WaitApplied = 6, ErrApplied = 7.",
		},
		bundledeploymentLabels,
	)

	bundleDeploymentObserved = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: bundledeploymentSubsystem,
			Name:      "total_observations",
			Help:      "The total times that this bundle deployment has been observed",
		},
		bundledeploymentLabels,
	)
)

func ObserveBundleDeployment(bundleDep *fleet.BundleDeployment, status *fleet.BundleDeploymentStatus) {
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
		"generation":           string(bundleDep.ObjectMeta.Generation),
	}

	bundleDeploymentState.With(labels).Set(float64(fleet.StateRank[summary.GetDeploymentState(bundleDep)]))
	bundleDeploymentObserved.With(labels).Inc()
}

func getClusterName(bundleDep *fleet.BundleDeployment) (string, string) {
	name, ok := bundleDep.Spec.StagedOptions.Helm.Values.Global.Fleet.ClusterLabels["management.cattle.io/cluster-name"]
	if !ok {
		name = ""
	}

	displayName, ok := bundleDep.Spec.StagedOptions.Helm.Values.Global.Fleet.ClusterLabels["management.cattle.io/cluster-display-name"]
	if !ok {
		displayName = ""
	}

	return name, displayName
}
