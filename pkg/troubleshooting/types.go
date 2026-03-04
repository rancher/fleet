package troubleshooting

import "time"

// Snapshot contains a point-in-time snapshot of Fleet resource diagnostic data.
type Snapshot struct {
	// Timestamp is an UTC timestamp of when the snapshot was created.
	Timestamp         string                 `json:"timestamp"`
	Controller        []ControllerInfo       `json:"controller,omitempty"`
	GitRepos          []GitRepoInfo          `json:"gitrepos,omitempty"`
	Bundles           []BundleInfo           `json:"bundles,omitempty"`
	BundleDeployments []BundleDeploymentInfo `json:"bundledeployments,omitempty"`
	Contents          []ContentInfo          `json:"contents,omitempty"`
	Clusters          []ClusterInfo          `json:"clusters,omitempty"`
	ClusterGroups     []ClusterGroupInfo     `json:"clustergroups,omitempty"`
	BundleSecrets     []SecretInfo           `json:"bundleSecrets,omitempty"`
	OrphanedSecrets   []SecretInfo           `json:"orphanedSecrets,omitempty"`
	ContentIssues     []ContentIssue         `json:"contentIssues,omitempty"`
	APIConsistency    *APIConsistency        `json:"apiConsistency,omitempty"`
	RecentEvents      []EventInfo            `json:"recentEvents,omitempty"`
	Diagnostics       *Diagnostics           `json:"diagnostics,omitempty"`
}

// ControllerInfo holds diagnostic information about a Fleet controller pod.
type ControllerInfo struct {
	Name      string `json:"name"`
	Restarts  int32  `json:"restarts"`
	Status    string `json:"status"`
	StartTime string `json:"startTime,omitempty"`
}

// GitRepoInfo holds diagnostic information about a Fleet GitRepo resource.
type GitRepoInfo struct {
	Namespace           string        `json:"namespace"`
	Name                string        `json:"name"`
	Generation          int64         `json:"generation"`
	ObservedGeneration  int64         `json:"observedGeneration,omitempty"`
	Commit              string        `json:"commit,omitempty"`
	PollingCommit       string        `json:"pollingCommit,omitempty"`
	WebhookCommit       string        `json:"webhookCommit,omitempty"`
	LastPollingTime     time.Time     `json:"lastPollingTime,omitempty"`
	PollingInterval     time.Duration `json:"pollingInterval,omitempty"`
	ForceSyncGeneration int64         `json:"forceSyncGeneration,omitempty"`
	Ready               bool          `json:"ready"`
	ReadyMessage        string        `json:"readyMessage,omitempty"`
}

// BundleInfo holds diagnostic information about a Fleet Bundle resource.
type BundleInfo struct {
	Namespace           string            `json:"namespace"`
	Name                string            `json:"name"`
	UID                 string            `json:"uid"`
	Generation          int64             `json:"generation"`
	ObservedGeneration  int64             `json:"observedGeneration,omitempty"`
	Commit              string            `json:"commit,omitempty"`
	RepoName            string            `json:"repoName,omitempty"`
	Labels              map[string]string `json:"labels,omitempty"`
	ForceSyncGeneration int64             `json:"forceSyncGeneration,omitempty"`
	ResourcesSHA256Sum  string            `json:"resourcesSHA256Sum,omitempty"`
	SizeBytes           *int64            `json:"sizeBytes,omitempty"`
	DeletionTimestamp   *string           `json:"deletionTimestamp,omitempty"`
	Finalizers          []string          `json:"finalizers,omitempty"`
	Ready               bool              `json:"ready"`
	ReadyMessage        string            `json:"readyMessage,omitempty"`
	ErrorMessage        string            `json:"errorMessage,omitempty"`
}

// Note: syncGeneration tracks forceSyncGeneration application, NOT resource generation.
// A BundleDeployment is stuck if:
//  1. forceSyncGeneration > 0 and syncGeneration != forceSyncGeneration (forced sync not applied)
//  2. deploymentID != appliedDeploymentID (new content not applied)
//  3. deletionTimestamp is set (being deleted but finalizers blocking)

// BundleDeploymentInfo holds diagnostic information about a Fleet BundleDeployment resource.
type BundleDeploymentInfo struct {
	Namespace           string            `json:"namespace"`
	Name                string            `json:"name"`
	UID                 string            `json:"uid"`
	Generation          int64             `json:"generation"`
	Commit              string            `json:"commit,omitempty"`
	ForceSyncGeneration int64             `json:"forceSyncGeneration,omitempty"`
	SyncGeneration      *int64            `json:"syncGeneration,omitempty"`
	DeploymentID        string            `json:"deploymentID,omitempty"`
	StagedDeploymentID  string            `json:"stagedDeploymentID,omitempty"`
	AppliedDeploymentID string            `json:"appliedDeploymentID,omitempty"`
	DeletionTimestamp   *string           `json:"deletionTimestamp,omitempty"`
	Finalizers          []string          `json:"finalizers,omitempty"`
	Ready               bool              `json:"ready"`
	ReadyMessage        string            `json:"readyMessage,omitempty"`
	ErrorMessage        string            `json:"errorMessage,omitempty"`
	Labels              map[string]string `json:"labels,omitempty"`
	BundleName          string            `json:"bundleName,omitempty"`
	BundleNamespace     string            `json:"bundleNamespace,omitempty"`
}

// ContentInfo holds diagnostic information about a Fleet Content resource.
type ContentInfo struct {
	Name              string   `json:"name"`
	Size              int64    `json:"size,omitempty"`
	DeletionTimestamp *string  `json:"deletionTimestamp,omitempty"`
	Finalizers        []string `json:"finalizers,omitempty"`
	ReferenceCount    int      `json:"referenceCount,omitempty"`
}

// SecretInfo holds diagnostic information about a bundle lifecycle secret.
type SecretInfo struct {
	Namespace         string   `json:"namespace"`
	Name              string   `json:"name"`
	Type              string   `json:"type"`
	Commit            string   `json:"commit,omitempty"`
	DeletionTimestamp *string  `json:"deletionTimestamp,omitempty"`
	Finalizers        []string `json:"finalizers,omitempty"`
	OwnerKind         string   `json:"ownerKind,omitempty"`
	OwnerName         string   `json:"ownerName,omitempty"`
	OwnerUID          string   `json:"ownerUID,omitempty"`
}

// EventInfo holds diagnostic information about a Kubernetes event.
type EventInfo struct {
	Namespace     string `json:"namespace"`
	Type          string `json:"type"`
	Reason        string `json:"reason"`
	Message       string `json:"message"`
	InvolvedKind  string `json:"involvedKind"`
	InvolvedName  string `json:"involvedName"`
	Count         int32  `json:"count"`
	LastTimestamp string `json:"lastTimestamp,omitempty"`
}

// ContentIssue describes a BundleDeployment with missing or problematic Content resources.
type ContentIssue struct {
	Namespace                string   `json:"namespace"`
	Name                     string   `json:"name"`
	ContentName              string   `json:"contentName,omitempty"`
	StagedContentName        string   `json:"stagedContentName,omitempty"`
	AppliedContentName       string   `json:"appliedContentName,omitempty"`
	ContentExists            bool     `json:"contentExists,omitempty"`
	ContentDeletionTimestamp *string  `json:"contentDeletionTimestamp,omitempty"`
	StagedContentExists      *bool    `json:"stagedContentExists,omitempty"`
	AppliedContentExists     *bool    `json:"appliedContentExists,omitempty"`
	Issues                   []string `json:"issues"`
}

// APIConsistency holds results of an API server consistency check.
type APIConsistency struct {
	Consistent bool     `json:"consistent"`
	Versions   []string `json:"versions"`
}

// Diagnostics is a comprehensive summary of all detected Fleet issues.
type Diagnostics struct {
	StuckBundleDeployments                      []BundleDeploymentInfo   `json:"stuckBundleDeployments,omitempty"`
	GitRepoBundleInconsistencies                []BundleInfo             `json:"gitRepoBundleInconsistencies,omitempty"`
	InvalidSecretOwners                         []SecretInfo             `json:"invalidSecretOwners,omitempty"`
	ResourcesWithMultipleFinalizers             []ResourceWithFinalizers `json:"resourcesWithMultipleFinalizers,omitempty"`
	LargeBundles                                []BundleInfo             `json:"largeBundles,omitempty"`
	BundlesWithMissingContent                   []BundleInfo             `json:"bundlesWithMissingContent,omitempty"`
	BundlesWithNoDeployments                    []BundleInfo             `json:"bundlesWithNoDeployments,omitempty"`
	GitReposWithNoBundles                       []GitRepoInfo            `json:"gitReposWithNoBundles,omitempty"`
	ClustersWithAgentIssues                     []ClusterInfo            `json:"clustersWithAgentIssues,omitempty"`
	ClusterGroupsWithNoClusters                 []ClusterGroupInfo       `json:"clusterGroupsWithNoClusters,omitempty"`
	BundlesWithMissingGitRepo                   []BundleInfo             `json:"bundlesWithMissingGitRepo,omitempty"`
	BundleDeploymentsWithMissingBundle          []BundleDeploymentInfo   `json:"bundleDeploymentsWithMissingBundle,omitempty"`
	GitReposWithCommitMismatch                  []GitRepoInfo            `json:"gitReposWithCommitMismatch,omitempty"`
	GitReposWithGenerationMismatch              []GitRepoInfo            `json:"gitReposWithGenerationMismatch,omitempty"`
	GitReposUnpolled                            []GitRepoInfo            `json:"gitReposUnpolled,omitempty"`
	BundlesWithGenerationMismatch               []BundleInfo             `json:"bundlesWithGenerationMismatch,omitempty"`
	BundleDeploymentsWithSyncGenerationMismatch []BundleDeploymentInfo   `json:"bundleDeploymentsWithSyncGenerationMismatch,omitempty"`
	OrphanedSecretsCount                        int                      `json:"orphanedSecretsCount,omitempty"`
	InvalidSecretOwnersCount                    int                      `json:"invalidSecretOwnersCount,omitempty"`
	ContentIssuesCount                          int                      `json:"contentIssuesCount,omitempty"`
	GitRepoBundleInconsistenciesCount           int                      `json:"gitRepoBundleInconsistenciesCount,omitempty"`
	ResourcesWithMultipleFinalizersCount        int                      `json:"resourcesWithMultipleFinalizersCount,omitempty"`
	BundlesWithDeletionTimestamp                int                      `json:"bundlesWithDeletionTimestamp,omitempty"`
	BundleDeploymentsWithDeletionTimestamp      int                      `json:"bundleDeploymentsWithDeletionTimestamp,omitempty"`
	ContentsWithDeletionTimestamp               int                      `json:"contentsWithDeletionTimestamp,omitempty"`
	ContentsWithZeroReferenceCount              int                      `json:"contentsWithZeroReferenceCount,omitempty"`
}

// ResourceWithFinalizers describes a resource with more than one finalizer.
type ResourceWithFinalizers struct {
	Kind              string   `json:"kind"`
	Namespace         string   `json:"namespace"`
	Name              string   `json:"name"`
	Finalizers        []string `json:"finalizers"`
	FinalizerCount    int      `json:"finalizerCount"`
	DeletionTimestamp *string  `json:"deletionTimestamp,omitempty"`
}

// ClusterInfo holds diagnostic information about a Fleet Cluster resource.
type ClusterInfo struct {
	Namespace              string  `json:"namespace"`
	Name                   string  `json:"name"`
	AgentNamespace         string  `json:"agentNamespace,omitempty"`
	AgentLastSeen          *string `json:"agentLastSeen,omitempty"`
	AgentLastSeenAge       string  `json:"agentLastSeenAge,omitempty"`
	Ready                  bool    `json:"ready"`
	ReadyMessage           string  `json:"readyMessage,omitempty"`
	BundleDeployments      int     `json:"bundleDeployments,omitempty"`
	ReadyBundleDeployments int     `json:"readyBundleDeployments,omitempty"`
}

// ClusterGroupInfo holds diagnostic information about a Fleet ClusterGroup resource.
type ClusterGroupInfo struct {
	Namespace    string `json:"namespace"`
	Name         string `json:"name"`
	ClusterCount int    `json:"clusterCount"`
	Selector     string `json:"selector,omitempty"`
}
