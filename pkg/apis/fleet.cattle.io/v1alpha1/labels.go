package v1alpha1

const (
	// FleetLabelPrefix is the reserved label namespace managed by Fleet
	// (equal to SchemeGroupVersion.Group + "/"). Labels under it are trusted
	// for internal bookkeeping and targeting; a downstream agent must not be
	// able to assert them during agent-initiated registration.
	FleetLabelPrefix = "fleet.cattle.io/"

	// ManagementLabelPrefix is the reserved label namespace managed by Rancher
	// (management.cattle.io). Labels under it - e.g.
	// management.cattle.io/cluster-display-name, consumed by clusterName
	// selectors - are trusted and must not be agent-assertable.
	ManagementLabelPrefix = "management.cattle.io/"

	// CreatedByAgentPodLabel is set by the agent on its own registration to
	// record the agent pod that created it, for debugging. Informational only;
	// never used for targeting or authorization. It is the one FleetLabelPrefix
	// label an agent is allowed to assert.
	CreatedByAgentPodLabel = "fleet.cattle.io/created-by-agent-pod"
)
