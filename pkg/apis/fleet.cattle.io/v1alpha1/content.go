package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Content is used internally by Fleet and should not be used directly. It
// contains the resources from a bundle for a specific target cluster.
type Content struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Content is a byte array, which contains the manifests of a bundle.
	// The bundle resources are copied into the bundledeployment's content
	// resource, so the downstream agent can deploy them.
	Content []byte `json:"content,omitempty"`
}
