package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

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
