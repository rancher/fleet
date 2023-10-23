package v1alpha1

import (
	"github.com/rancher/wrangler/v2/pkg/genericcondition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ClusterGroup is a re-usable selector to target a group of clusters.
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
