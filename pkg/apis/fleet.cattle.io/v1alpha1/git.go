package v1alpha1

import (
	"github.com/rancher/wrangler/pkg/genericcondition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type GitRepo struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GitRepoSpec   `json:"spec,omitempty"`
	Status GitRepoStatus `json:"status,omitempty"`
}

type GitRepoSpec struct {
	// Repo is a URL to a git repo to clone and index
	Repo string `json:"repo,omitempty"`

	// Branch The git branch to follow
	Branch string `json:"branch,omitempty"`

	// Revision A specific commit or tag to operate on
	Revision string `json:"revision,omitempty"`

	// ClientSecretName is the client secret to be used to connect to the repo
	// It is expected the secret be of type "kubernetes.io/basic-auth" or "kubernetes.io/ssh-auth".
	ClientSecretName string `json:"clientSecretName,omitempty"`

	// BundleDirs is the directories relative to the git repo root that contain bundles to be applied.
	// Path globbing is support, for example ["bundles/*"] will match all folders as a subdirectory of bundles/
	// If empty, "/" is the default
	BundleDirs []string `json:"bundleDirs,omitempty"`

	// ServiceAccount used in the downstream cluster for deployment
	ServiceAccount string `json:"serviceAccount,omitempty"`

	// Targets is a list of target this repo will deploy to
	Targets []GitTarget `json:"targets,omitempty"`
}

type GitTarget struct {
	Name                 string                `json:"name,omitempty"`
	ClusterSelector      *metav1.LabelSelector `json:"clusterSelector,omitempty"`
	ClusterGroup         string                `json:"clusterGroup,omitempty"`
	ClusterGroupSelector *metav1.LabelSelector `json:"clusterGroupSelector,omitempty"`
}

type GitRepoStatus struct {
	ObservedGeneration int64                               `json:"observedGeneration"`
	Commit             string                              `json:"commit,omitempty"`
	Summary            BundleSummary                       `json:"summary,omitempty"`
	Display            GitRepoDisplay                      `json:"display,omitempty"`
	Conditions         []genericcondition.GenericCondition `json:"conditions,omitempty"`
}

type GitRepoDisplay struct {
	ReadyBundleDeployments string `json:"readyBundleDeployments,omitempty"`
	State                  string `json:"state,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type GitRepoRestriction struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	DefaultServiceAccount  string   `json:"defaultServiceAccount,omitempty"`
	AllowedServiceAccounts []string `json:"allowedServiceAccounts,omitempty"`
	AllowedRepoPatterns    []string `json:"allowedRepoPatterns,omitempty"`

	DefaultClientSecretName  string   `json:"defaultClientSecretName,omitempty"`
	AllowedClientSecretNames []string `json:"allowedClientSecretNames,omitempty"`
}
