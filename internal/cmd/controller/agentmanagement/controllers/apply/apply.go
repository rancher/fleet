package apply

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Apply provides Server-Side Apply functionality similar to wrangler's apply.Apply
// but using controller-runtime's client
type Apply struct {
	client           client.Client
	fieldManager     string
	noDeleteGVKs     map[schema.GroupVersionKind]bool
	setOwnerRef      bool
	setOwnerRefBlock bool
	defaultNamespace string
}

// NewApply creates a new Apply instance
func NewApply(c client.Client, fieldManager string) *Apply {
	return &Apply{
		client:       c,
		fieldManager: fieldManager,
		noDeleteGVKs: make(map[schema.GroupVersionKind]bool),
	}
}

// WithSetID sets the field manager (equivalent to SetID in wrangler)
func (a *Apply) WithSetID(id string) *Apply {
	return &Apply{
		client:           a.client,
		fieldManager:     id,
		noDeleteGVKs:     a.noDeleteGVKs,
		setOwnerRef:      a.setOwnerRef,
		setOwnerRefBlock: a.setOwnerRefBlock,
		defaultNamespace: a.defaultNamespace,
	}
}

// WithDefaultNamespace sets the default namespace for resources
func (a *Apply) WithDefaultNamespace(ns string) *Apply {
	return &Apply{
		client:           a.client,
		fieldManager:     a.fieldManager,
		noDeleteGVKs:     a.noDeleteGVKs,
		setOwnerRef:      a.setOwnerRef,
		setOwnerRefBlock: a.setOwnerRefBlock,
		defaultNamespace: ns,
	}
}

// WithSetOwnerReference configures owner reference behavior
func (a *Apply) WithSetOwnerReference(controller, block bool) *Apply {
	return &Apply{
		client:           a.client,
		fieldManager:     a.fieldManager,
		noDeleteGVKs:     a.noDeleteGVKs,
		setOwnerRef:      controller,
		setOwnerRefBlock: block,
		defaultNamespace: a.defaultNamespace,
	}
}

// WithNoDeleteGVK marks GVKs that should not be deleted
func (a *Apply) WithNoDeleteGVK(gvks ...schema.GroupVersionKind) *Apply {
	newNoDelete := make(map[schema.GroupVersionKind]bool)
	for k, v := range a.noDeleteGVKs {
		newNoDelete[k] = v
	}
	for _, gvk := range gvks {
		newNoDelete[gvk] = true
	}
	return &Apply{
		client:           a.client,
		fieldManager:     a.fieldManager,
		noDeleteGVKs:     newNoDelete,
		setOwnerRef:      a.setOwnerRef,
		setOwnerRefBlock: a.setOwnerRefBlock,
		defaultNamespace: a.defaultNamespace,
	}
}

// ApplyObjects applies the given objects using Server-Side Apply
func (a *Apply) ApplyObjects(ctx context.Context, objs ...runtime.Object) error {
	if a.fieldManager == "" {
		return fmt.Errorf("field manager must be set")
	}

	for _, obj := range objs {
		if err := a.applyObject(ctx, obj); err != nil {
			return err
		}
	}
	return nil
}

func (a *Apply) applyObject(ctx context.Context, obj runtime.Object) error {
	// Convert to client.Object
	clientObj, ok := obj.(client.Object)
	if !ok {
		return fmt.Errorf("object does not implement client.Object: %T", obj)
	}

	// Ensure GVK is set - required for Server-Side Apply
	gvk := clientObj.GetObjectKind().GroupVersionKind()
	if gvk.Kind == "" {
		// Try to get GVK from the client's scheme
		gvks, _, err := a.client.Scheme().ObjectKinds(obj)
		if err != nil {
			return fmt.Errorf("failed to get GVK for object %T: %w", obj, err)
		}
		if len(gvks) == 0 {
			return fmt.Errorf("no GVK found for object %T", obj)
		}
		clientObj.GetObjectKind().SetGroupVersionKind(gvks[0])
		gvk = gvks[0]
	}

	// Set default namespace if not set
	if clientObj.GetNamespace() == "" && a.defaultNamespace != "" {
		// Check if this is a namespaced resource
		if gvk.Kind != "Namespace" && gvk.Kind != "ClusterRole" && gvk.Kind != "ClusterRoleBinding" {
			clientObj.SetNamespace(a.defaultNamespace)
		}
	}

	// Use Server-Side Apply with force to take ownership
	err := a.client.Patch(ctx, clientObj, client.Apply, client.ForceOwnership, client.FieldOwner(a.fieldManager))
	if err != nil {
		return fmt.Errorf("failed to apply %s %s/%s: %w",
			gvk.Kind,
			clientObj.GetNamespace(),
			clientObj.GetName(),
			err)
	}

	return nil
}

// DeleteManagedObjects deletes objects that were previously managed by this field manager
// but are not in the current set of objects
func (a *Apply) DeleteManagedObjects(ctx context.Context, namespace string, gvk schema.GroupVersionKind, currentObjects map[string]bool) error {
	// Skip deletion for GVKs marked as no-delete
	if a.noDeleteGVKs[gvk] {
		return nil
	}

	// List all objects of this type
	list := &metav1.PartialObjectMetadataList{}
	list.SetGroupVersionKind(gvk)

	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}

	if err := a.client.List(ctx, list, opts...); err != nil {
		if meta.IsNoMatchError(err) || errors.IsNotFound(err) {
			// Resource type doesn't exist, skip
			return nil
		}
		return err
	}

	// Delete objects that are not in the current set
	for _, item := range list.Items {
		// Check if this object is managed by our field manager
		managedByUs := false
		for _, managedField := range item.GetManagedFields() {
			if managedField.Manager == a.fieldManager {
				managedByUs = true
				break
			}
		}

		if !managedByUs {
			continue
		}

		// Check if this object is in the current set
		key := item.GetNamespace() + "/" + item.GetName()
		if currentObjects[key] {
			continue
		}

		// Delete the object
		if err := a.client.Delete(ctx, &item); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete %s %s: %w", gvk.Kind, key, err)
		}
	}

	return nil
}
