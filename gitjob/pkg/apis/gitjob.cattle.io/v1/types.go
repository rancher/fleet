package v1

import (
	"github.com/rancher/wrangler/v2/pkg/genericcondition"
	v1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type GitJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              GitJobSpec   `json:"spec,omitempty"`
	Status            GitJobStatus `json:"status,omitempty"`
}

type GitEvent struct {
	// The latest commit SHA received from git repo
	Commit string `json:"commit,omitempty" column:"name=COMMIT,type=string,jsonpath=.status.commit"`

	// Last executed commit SHA by gitjob controller
	LastExecutedCommit string `json:"lastExecutedCommit,omitempty"`

	// Last sync time
	LastSyncedTime metav1.Time `json:"lastSyncedTime,omitempty"`

	GithubMeta
}

type GithubMeta struct {
	// Github webhook ID. Internal use only. If not empty, means a webhook is created along with this CR
	HookID string `json:"hookId,omitempty"`

	// Github webhook validation token to validate requests that are only coming from github
	ValidationToken string `json:"secretToken,omitempty"`

	// Last received github webhook event
	Event string `json:"event,omitempty"`
}

type GitJobSpec struct {
	// Git metadata information
	Git GitInfo `json:"git,omitempty"`

	// Job template applied to git commit
	JobSpec v1.JobSpec `json:"jobSpec,omitempty"`

	// define interval(in seconds) for controller to sync repo and fetch commits
	SyncInterval int `json:"syncInterval,omitempty"`

	// ForceUpdate is a timestamp where can be set to do a force re-sync. If it is after the last synced timestamp and before the current timestamp it will be re-synced
	ForceUpdateGeneration int64 `json:"forceUpdateGeneration,omitempty"`
}

type GitInfo struct {
	// Git credential metadata
	Credential

	// Git provider model to fetch commit. Can be polling(regular git fetch)/webhook(github webhook)
	Provider string `json:"provider,omitempty"`

	// Git repo URL
	Repo string `json:"repo,omitempty" column:"name=REPO,type=string,jsonpath=.spec.git.repo"`

	// Git commit SHA. If specified, controller will use this SHA instead of auto-fetching commit
	Revision string `json:"revision,omitempty"`

	// Git branch to watch. Default to master
	Branch string `json:"branch,omitempty" column:"name=BRANCH,type=string,jsonpath=.spec.git.branch"`

	// Semver matching for incoming tag event
	OnTag string `json:"onTag,omitempty"`
}

type Credential struct {
	// CABundle is a PEM encoded CA bundle which will be used to validate the repo's certificate.
	CABundle []byte `json:"caBundle,omitempty"`

	// InsecureSkipTLSverify will use insecure HTTPS to download the repo's index.
	InsecureSkipTLSverify bool `json:"insecureSkipTLSVerify,omitempty"`

	// Secret Name of git credential
	ClientSecretName string `json:"clientSecretName,omitempty"`
}

type GitJobStatus struct {
	GitEvent

	// Status of job launched by controller
	JobStatus string `json:"jobStatus,omitempty" column:"name=JOBSTATUS,type=string,jsonpath=.status.jobStatus"`

	// Generation of status to indicate if resource is out-of-sync
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Update generation is the force update generation if spec.forceUpdateGeneration is set
	UpdateGeneration int64 `json:"updateGeneration,omitempty"`

	// Condition of the resource
	Conditions []genericcondition.GenericCondition `json:"conditions,omitempty"`
}
