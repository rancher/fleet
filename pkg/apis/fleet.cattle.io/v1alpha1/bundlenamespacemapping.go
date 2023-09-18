package v1alpha1

import "k8s.io/apimachinery/pkg/apis/meta/v1"

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// BundleNamespaceMapping maps bundles to clusters in other namespaces.
type BundleNamespaceMapping struct {
	v1.TypeMeta   `json:",inline"`
	v1.ObjectMeta `json:"metadata,omitempty"`

	BundleSelector    *v1.LabelSelector `json:"bundleSelector,omitempty"`
	NamespaceSelector *v1.LabelSelector `json:"namespaceSelector,omitempty"`
}
