package v1alpha1

import (
	"github.com/rancher/wrangler/pkg/genericcondition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	ClusterConditionReady           = "Ready"
	ClusterGroupAnnotation          = "fleet.cattle.io/cluster-group"
	ClusterGroupTokenAnnotation     = "fleet.cattle.io/cluster-group-token"
	ClusterGroupNamespaceAnnotation = "fleet.cattle.io/cluster-group-namespace"
	ClusterNamespaceAnnotation      = "fleet.cattle.io/cluster-namespace"
	ClusterAnnotation               = "fleet.cattle.io/cluster"
	RequestAnnotation               = "fleet.cattle.io/request"
	TTLSecondsAnnotation            = "fleet.cattle.io/ttl-seconds"
	ManagedAnnotation               = "fleet.cattle.io/managed"
	AnnotationGroup                 = "fleet.cattle.io/"

	BootstrapToken = "fleet.cattle.io/bootstrap-token"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ClusterGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterGroupSpec   `json:"spec"`
	Status ClusterGroupStatus `json:"status"`
}

type ClusterGroupSpec struct {
	Pause bool `json:"pause,omitempty"`
}

type ClusterGroupStatus struct {
	Namespace            string                              `json:"namespace,omitempty"`
	ClusterCount         int                                 `json:"clusterCount"`
	NonReadyClusterCount int                                 `json:"nonReadyClusterCount"`
	NonReadyClusters     []string                            `json:"nonReadyClusters,omitempty"`
	Conditions           []genericcondition.GenericCondition `json:"conditions,omitempty"`
	Summary              BundleSummary                       `json:"summary,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Cluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterSpec   `json:"spec,omitempty"`
	Status ClusterStatus `json:"status,omitempty"`
}

type ClusterSpec struct {
	Paused bool `json:"paused,omitempty"`
}

type ClusterStatus struct {
	Conditions            []genericcondition.GenericCondition `json:"conditions,omitempty"`
	ClusterGroupName      string                              `json:"clusterGroupName,omitempty"`
	ClusterGroupNamespace string                              `json:"clusterGroupNamespace,omitempty"`
	Namespace             string                              `json:"namespace,omitempty"`
	Summary               BundleSummary                       `json:"summary,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ClusterRegistrationRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterRegistrationRequestSpec   `json:"spec,omitempty"`
	Status ClusterRegistrationRequestStatus `json:"status,omitempty"`
}

type ClusterRegistrationRequestSpec struct {
	ClientID      string            `json:"clientID,omitempty"`
	ClientRandom  string            `json:"clientRandom,omitempty"`
	ClusterLabels map[string]string `json:"clusterLabels,omitempty"`
}

type ClusterRegistrationRequestStatus struct {
	ClusterName      string `json:"clusterName,omitempty"`
	ClusterNamespace string `json:"clusterNamespace,omitempty"`
	Granted          bool   `json:"granted,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ClusterGroupToken struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterGroupTokenSpec   `json:"spec,omitempty"`
	Status ClusterGroupTokenStatus `json:"status,omitempty"`
}

type ClusterGroupTokenSpec struct {
	TTLSeconds       int    `json:"ttlSeconds,omitempty"`
	ClusterGroupName string `json:"clusterGroupName,omitempty"`
}

type ClusterGroupTokenStatus struct {
	SecretName string `json:"secretName,omitempty"`
}
