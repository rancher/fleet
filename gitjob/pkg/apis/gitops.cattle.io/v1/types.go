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
	Spec              GitJobSpec   `json:"spec,omitempty"`
	Status            GitJobStatus `json:"status,omitempty"`
}

type GitEvent struct {
	Commit string `json:"commit,omitempty"`

	*GithubMeta
}

type GithubMeta struct {
	Initialized bool   `json:"initialized,omitempty"`
	Event       string `json:"event,omitempty"`
}

type GitJobSpec struct {
	Git     GitInfo    `json:"git,omitempty"`
	JobSpec v1.JobSpec `json:"jobSpec,omitempty"`
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
	GitHostname   string `json:"gitHostName,omitempty"`
	GitSecretName string `json:"gitSecretName,omitempty"`
	GitSecretType string `json:"gitSecretType,omitempty"`
}

type GitJobStatus struct {
	GitEvent
	Conditions []genericcondition.GenericCondition `json:"conditions,omitempty"`
}
