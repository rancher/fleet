package v1alpha1

// Resource contains metadata about the resources of a bundle.
type Resource struct {
	// APIVersion is the API version of the resource.
	// +nullable
	APIVersion string `json:"apiVersion,omitempty"`
	// Kind is the k8s kind of the resource.
	// +nullable
	Kind string `json:"kind,omitempty"`
	// Type is the type of the resource, e.g. "apiextensions.k8s.io.customresourcedefinition" or "configmap".
	Type string `json:"type,omitempty"`
	// ID is the name of the resource, e.g. "namespace1/my-config" or "backingimagemanagers.storage.io".
	// +nullable
	ID string `json:"id,omitempty"`
	// Namespace of the resource.
	// +nullable
	Namespace string `json:"namespace,omitempty"`
	// Name of the resource.
	// +nullable
	Name string `json:"name,omitempty"`
	// IncompleteState is true if a bundle summary has 10 or more non-ready
	// resources or a non-ready resource has more 10 or more non-ready or
	// modified states.
	IncompleteState bool `json:"incompleteState,omitempty"`
	// State is the state of the resource, e.g. "Unknown", "WaitApplied", "ErrApplied" or "Ready".
	State string `json:"state,omitempty"`
	// Error is true if any Error in the PerClusterState is true.
	Error bool `json:"error,omitempty"`
	// Transitioning is true if any Transitioning in the PerClusterState is true.
	Transitioning bool `json:"transitioning,omitempty"`
	// Message is the first message from the PerClusterStates.
	// +nullable
	Message string `json:"message,omitempty"`
	// PerClusterState is a list of states for each cluster. Derived from the summaries non-ready resources.
	// +nullable
	PerClusterState []ResourcePerClusterState `json:"perClusterState,omitempty"`
}

// ResourceCounts contains the number of resources in each state.
type ResourceCounts struct {
	// Ready is the number of ready resources.
	// +optional
	Ready int `json:"ready"`
	// DesiredReady is the number of resources that should be ready.
	// +optional
	DesiredReady int `json:"desiredReady"`
	// WaitApplied is the number of resources that are waiting to be applied.
	// +optional
	WaitApplied int `json:"waitApplied"`
	// Modified is the number of resources that have been modified.
	// +optional
	Modified int `json:"modified"`
	// Orphaned is the number of orphaned resources.
	// +optional
	Orphaned int `json:"orphaned"`
	// Missing is the number of missing resources.
	// +optional
	Missing int `json:"missing"`
	// Unknown is the number of resources in an unknown state.
	// +optional
	Unknown int `json:"unknown"`
	// NotReady is the number of not ready resources. Resources are not
	// ready if they do not match any other state.
	// +optional
	NotReady int `json:"notReady"`
}