package v1

import (
	"github.com/rancher/wrangler/pkg/genericcondition"
	v1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type GitJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              GitjobSpec   `json:"spec,omitempty"`
	Status            GitjobStatus `json:"status,omitempty"`
}

type GitEvent struct {
	Commit string `json:"commit,omitempty"`

	*GithubMeta
}

type GithubMeta struct {
	Initialized bool   `json:"initialized,omitempty"`
	Event       string `json:"event,omitempty"`
}

type GitjobSpec struct {
	Git     GitInfo    `json:"git,omitempty"`
	JobSpec v1.JobSpec `json:"jobSpec,omitempty"`

	// define interval(in seconds) for controller to sync repo and fetch commits
	SyncInterval int `json:"syncInterval,omitempty"`
}

type GitInfo struct {
	Credential
	Provider string `json:"provider,omitempty"`
	Repo     string `json:"repo,omitempty"`
	Revision string `json:"revision,omitempty"`
	Branch   string `json:"branch,omitempty"`

	Github
}

type Github struct {
	// Secret Token is used to validate if payload is coming from github
	Token string `json:"secret,omitempty"`
}

type Credential struct {
	// CABundle is a PEM encoded CA bundle which will be used to validate the repo's certificate.
	CABundle []byte `json:"caBundle,omitempty"`

	// InsecureSkipTLSverify will use insecure HTTPS to download the repo's index.
	InsecureSkipTLSverify bool `json:"insecureSkipTLSVerify,omitempty"`

	// Hostname of git server
	GitHostname string `json:"gitHostName,omitempty"`

	// Secret Name of git credential
	GitSecretName string `json:"gitSecretName,omitempty"`
}

type GitjobStatus struct {
	GitEvent
	Conditions []genericcondition.GenericCondition `json:"conditions,omitempty"`
}
