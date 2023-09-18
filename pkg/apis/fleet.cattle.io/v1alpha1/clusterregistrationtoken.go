package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

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
