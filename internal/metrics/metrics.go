package metrics

import fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

var (
	// The namespace for the metrics, not the Kubernetes namespace of the
	// resources. This is the prefix for the metric (e.g. `fleet_` for value
	// `fleet`).
	namespace    = "fleet"
	bundleStates = []fleet.BundleState{
		fleet.Ready,
		fleet.NotReady,
		fleet.Pending,
		fleet.OutOfSync,
		fleet.Modified,
		fleet.WaitApplied,
		fleet.ErrApplied,
	}
	commitLabel   = "fleet.cattle.io/commit"
	repoNameLabel = "fleet.cattle.io/repo-name"
	enabled       = false
)

func RegisterMetrics() {
	enabled = true
	registerBundleDeploymentMetrics()
	registerBundleMetrics()
	registerClusterGroupMetrics()
	registerClusterMetrics()
	registerGitRepoMetrics()
}
