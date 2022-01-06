package metrics

import fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

var (
	namespace = "fleet"

	bundleStates = []fleet.BundleState{
		fleet.Ready,
		fleet.NotReady,
		fleet.Pending,
		fleet.OutOfSync,
		fleet.Modified,
		fleet.WaitApplied,
		fleet.ErrApplied,
	}

	clusterStates = []string{
		string(fleet.NotReady),
		string(fleet.Ready),
		"WaitCheckIn",
	}

	clusterGroupStates = []string{
		string(fleet.NotReady),
		string(fleet.Ready),
	}

	clusterNameLabel        = "management.cattle.io/cluster-name"
	clusterDisplayNameLabel = "management.cattle.io/cluster-display-name"
)
