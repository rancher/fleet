package metrics

import fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

var (
	namespace = "fleet"

	states = []fleet.BundleState{
		fleet.Ready,
		fleet.NotReady,
		fleet.Pending,
		fleet.OutOfSync,
		fleet.Modified,
		fleet.WaitApplied,
		fleet.ErrApplied,
	}

	clusterNameLabel        = "management.cattle.io/cluster-name"
	clusterDisplayNameLabel = "management.cattle.io/cluster-display-name"
)
