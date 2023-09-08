package v1alpha1

import (
	"github.com/rancher/wrangler/pkg/genericcondition"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	// ClusterConditionReady indicates that all bundles in this cluster
	// have been deployed and all resources are ready.
	ClusterConditionReady = "Ready"
	// ClusterNamespaceAnnotation used on a cluster namespace to refer to
	// the cluster registration namespace, which contains the cluster
	// resource.
	ClusterNamespaceAnnotation = "fleet.cattle.io/cluster-namespace"
	// ClusterAnnotation used on a cluster namespace to refer to the
	// cluster name for that namespace.
	ClusterAnnotation = "fleet.cattle.io/cluster"
	// ClusterRegistrationAnnotation is the name of the
	// ClusterRegistration, it's added to the request service account.
	ClusterRegistrationAnnotation = "fleet.cattle.io/cluster-registration"
	// ClusterRegistrationTokenAnnotation is the namespace of the
	// clusterregistration, e.g. "fleet-local".
	ClusterRegistrationNamespaceAnnotation = "fleet.cattle.io/cluster-registration-namespace"
	// ManagedLabel is used for clean up. Cluster namespaces and other
	// resources with this label will be cleaned up. Used in Rancher to
	// identify fleet namespaces.
	ManagedLabel = "fleet.cattle.io/managed"
	// ClusterNamespaceLabel is used on a bundledeployment to refer to the
	// cluster registration namespace of the targeted cluster.
	ClusterNamespaceLabel = "fleet.cattle.io/cluster-namespace"
	// ClusterLabel is used on a bundledeployment to refer to the targeted
	// cluster
	ClusterLabel = "fleet.cattle.io/cluster"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterGroup is a stored selector to target a group of clusters.
type ClusterGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterGroupSpec   `json:"spec"`
	Status ClusterGroupStatus `json:"status"`
}

type ClusterGroupSpec struct {
	// Selector is a label selector, used to select clusters for this group.
	Selector *metav1.LabelSelector `json:"selector,omitempty"`
}

type ClusterGroupStatus struct {
	// ClusterCount is the number of clusters in the cluster group.
	ClusterCount int `json:"clusterCount"`
	// NonReadyClusterCount is the number of clusters that are not ready.
	NonReadyClusterCount int `json:"nonReadyClusterCount"`
	// NonReadyClusters is a list of cluster names that are not ready.
	NonReadyClusters []string `json:"nonReadyClusters,omitempty"`
	// Conditions is a list of conditions and their statuses for the cluster group.
	Conditions []genericcondition.GenericCondition `json:"conditions,omitempty"`
	// Summary is a summary of the bundle deployments and their resources
	// in the cluster group.
	Summary BundleSummary `json:"summary,omitempty"`
	// Display contains the number of ready, desiredready clusters and a
	// summary state for the bundle's resources.
	Display ClusterGroupDisplay `json:"display,omitempty"`
	// ResourceCounts contains the number of resources in each state over
	// all bundles in the cluster group.
	ResourceCounts GitRepoResourceCounts `json:"resourceCounts,omitempty"`
}

type ClusterGroupDisplay struct {
	// ReadyClusters is a string in the form "%d/%d", that describes the
	// number of clusters that are ready vs. the number of clusters desired
	// to be ready.
	ReadyClusters string `json:"readyClusters,omitempty"`
	// ReadyBundles is a string in the form "%d/%d", that describes the
	// number of bundles that are ready vs. the number of bundles desired
	// to be ready.
	ReadyBundles string `json:"readyBundles,omitempty"`
	// State is a summary state for the cluster group, showing "NotReady" if
	// there are non-ready resources.
	State string `json:"state,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Cluster corresponds to a Kubernetes cluster. Fleet deploys bundles to targeted clusters.
// Clusters to which Fleet deploys manifests are referred to as downstream
// clusters. In the single cluster use case, the Fleet manager Kubernetes
// cluster is both the manager and downstream cluster at the same time.
type Cluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterSpec   `json:"spec,omitempty"`
	Status ClusterStatus `json:"status,omitempty"`
}

type ClusterSpec struct {
	// Paused if set to true, will stop any BundleDeployments from being updated.
	Paused bool `json:"paused,omitempty"`

	// ClientID is a unique string that will identify the cluster. It can
	// either be predefined, or generated when importing the cluster.
	ClientID string `json:"clientID,omitempty"`

	// KubeConfigSecret is the name of the secret containing the kubeconfig for the downstream cluster.
	KubeConfigSecret string `json:"kubeConfigSecret,omitempty"`

	// RedeployAgentGeneration can be used to force redeploying the agent.
	RedeployAgentGeneration int64 `json:"redeployAgentGeneration,omitempty"`

	// AgentEnvVars are extra environment variables to be added to the agent deployment.
	AgentEnvVars []v1.EnvVar `json:"agentEnvVars,omitempty"`

	// AgentNamespace defaults to the system namespace, e.g. cattle-fleet-system.
	AgentNamespace string `json:"agentNamespace,omitempty"`

	// PrivateRepoURL prefixes the image name and overrides a global repo URL from the agents config.
	PrivateRepoURL string `json:"privateRepoURL,omitempty"`

	// TemplateValues defines a cluster specific mapping of values to be sent to fleet.yaml values templating.
	TemplateValues *GenericMap `json:"templateValues,omitempty"`

	// AgentTolerations defines an extra set of Tolerations to be added to the Agent deployment.
	AgentTolerations []v1.Toleration `json:"agentTolerations,omitempty"`

	// AgentAffinity overrides the default affinity for the cluster's agent
	// deployment. If this value is nil the default affinity is used.
	AgentAffinity *v1.Affinity `json:"agentAffinity,omitempty"`

	// AgentResources sets the resources for the cluster's agent deployment.
	AgentResources *v1.ResourceRequirements `json:"agentResources,omitempty"`
}

type ClusterStatus struct {
	Conditions []genericcondition.GenericCondition `json:"conditions,omitempty"`

	// Namespace is the cluster namespace, it contains the clusters service
	// account as well as any bundledeployments. Example:
	// "cluster-fleet-local-cluster-294db1acfa77-d9ccf852678f"
	Namespace string `json:"namespace,omitempty"`

	// Summary is a summary of the bundledeployments. The resource counts
	// are copied from the gitrepo resource.
	Summary BundleSummary `json:"summary,omitempty"`
	// ResourceCounts is an aggregate over the GitRepoResourceCounts.
	ResourceCounts GitRepoResourceCounts `json:"resourceCounts,omitempty"`
	// ReadyGitRepos is the number of gitrepos for this cluster that are ready.
	ReadyGitRepos int `json:"readyGitRepos"`
	// DesiredReadyGitRepos is the number of gitrepos for this cluster that
	// are desired to be ready.
	DesiredReadyGitRepos int `json:"desiredReadyGitRepos"`

	// AgentEnvVarsHash is a hash of the agent's env vars, used to detect changes.
	AgentEnvVarsHash string `json:"agentEnvVarsHash,omitempty"`
	// AgentPrivateRepoURL is the private repo URL for the agent that is currently used.
	AgentPrivateRepoURL string `json:"agentPrivateRepoURL,omitempty"`
	// AgentDeployedGeneration is the generation of the agent that is currently deployed.
	AgentDeployedGeneration *int64 `json:"agentDeployedGeneration,omitempty"`
	// AgentMigrated is always set to true after importing a cluster. If
	// false, it will trigger a migration. Old agents don't have
	// this in their status.
	AgentMigrated bool `json:"agentMigrated,omitempty"`
	// AgentNamespaceMigrated is always set to true after importing a
	// cluster. If false, it will trigger a migration. Old Fleet agents
	// don't have this in their status.
	AgentNamespaceMigrated bool `json:"agentNamespaceMigrated,omitempty"`
	// CattleNamespaceMigrated is always set to true after importing a
	// cluster. If false, it will trigger a migration. Old Fleet agents,
	// don't have this in their status.
	CattleNamespaceMigrated bool `json:"cattleNamespaceMigrated,omitempty"`

	// AgentAffinityHash is a hash of the agent's affinity configuration,
	// used to detect changes.
	AgentAffinityHash string `json:"agentAffinityHash,omitempty"`
	// AgentResourcesHash is a hash of the agent's resources configuration,
	// used to detect changes.
	AgentResourcesHash string `json:"agentResourcesHash,omitempty"`
	// AgentTolerationsHash is a hash of the agent's tolerations
	// configuration, used to detect changes.
	AgentTolerationsHash string `json:"agentTolerationsHash,omitempty"`
	// AgentConfigChanged is set to true if any of the agent configuration
	// changed, like the API server URL or CA. Setting it to true will
	// trigger a re-import of the cluster.
	AgentConfigChanged bool `json:"agentConfigChanged,omitempty"`

	// APIServerURL is the currently used URL of the API server that the
	// cluster uses to connect to upstream.
	APIServerURL string `json:"apiServerURL,omitempty"`
	// APIServerCAHash is a hash of the upstream API server CA, used to detect changes.
	APIServerCAHash string `json:"apiServerCAHash,omitempty"`

	// Display contains the number of ready bundles, nodes and a summary state.
	Display ClusterDisplay `json:"display,omitempty"`
	// AgentStatus contains information about the agent.
	Agent AgentStatus `json:"agent,omitempty"`
}

type ClusterDisplay struct {
	// ReadyBundles is a string in the form "%d/%d", that describes the
	// number of bundles that are ready vs. the number of bundles desired
	// to be ready.
	ReadyBundles string `json:"readyBundles,omitempty"`
	// ReadyNodes is a string in the form "%d/%d", that describes the
	// number of nodes that are ready vs. the number of expected nodes.
	ReadyNodes string `json:"readyNodes,omitempty"`
	// SampleNode is the name of one of the nodes that are ready. If no
	// node is ready, it's the name of a node that is not ready.
	SampleNode string `json:"sampleNode,omitempty"`
	// State of the cluster, either one of the bundle states, or "WaitCheckIn".
	State string `json:"state,omitempty"`
}

type AgentStatus struct {
	// LastSeen is the last time the agent checked in to update the status
	// of the cluster resource.
	LastSeen metav1.Time `json:"lastSeen"`
	// Namespace is the namespace of the agent deployment, e.g. "cattle-fleet-system".
	Namespace string `json:"namespace"`
	// NonReadyNodes is the number of nodes that are not ready.
	NonReadyNodes int `json:"nonReadyNodes"`
	// ReadyNodes is the number of nodes that are ready.
	ReadyNodes int `json:"readyNodes"`
	// NonReadyNode contains the names of non-ready nodes. The list is
	// limited to at most 3 names.
	NonReadyNodeNames []string `json:"nonReadyNodeNames"`
	// ReadyNodes contains the names of ready nodes. The list is limited to
	// at most 3 names.
	ReadyNodeNames []string `json:"readyNodeNames"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterRegistration is used internally by Fleet and should not be used directly.
type ClusterRegistration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterRegistrationSpec   `json:"spec,omitempty"`
	Status ClusterRegistrationStatus `json:"status,omitempty"`
}

type ClusterRegistrationSpec struct {
	// ClientID is a unique string that will identify the cluster. The
	// agent either uses the configured ID or the kubeSystem.UID.
	ClientID string `json:"clientID,omitempty"`
	// ClientRandom is a random string that the agent generates. When
	// fleet-controller grants a registration, it creates a registration
	// secret with this string in the name.
	ClientRandom string `json:"clientRandom,omitempty"`
	// ClusterLabels are copied to the cluster resource during the registration.
	ClusterLabels map[string]string `json:"clusterLabels,omitempty"`
}

type ClusterRegistrationStatus struct {
	// ClusterName is only set after the registration is being processed by
	// fleet-controller.
	ClusterName string `json:"clusterName,omitempty"`
	// Granted is set to true, if the request service account is present
	// and its token secret exists. This happens directly before creating
	// the registration secret, roles and rolebindings.
	Granted bool `json:"granted,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterRegistrationToken is used by agents to register a new cluster.
type ClusterRegistrationToken struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterRegistrationTokenSpec   `json:"spec,omitempty"`
	Status ClusterRegistrationTokenStatus `json:"status,omitempty"`
}

type ClusterRegistrationTokenSpec struct {
	// TTL is the time to live for the token. It is used to calculate the
	// expiration time. If the token expires, it will be deleted.
	TTL *metav1.Duration `json:"ttl,omitempty"`
}

type ClusterRegistrationTokenStatus struct {
	// Expires is the time when the token expires.
	Expires *metav1.Time `json:"expires,omitempty"`
	// SecretName is the name of the secret containing the token.
	SecretName string `json:"secretName,omitempty"`
}
