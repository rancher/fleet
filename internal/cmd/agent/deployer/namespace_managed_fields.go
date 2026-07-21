package deployer

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/structured-merge-diff/v6/fieldpath"
)

// legacyNamespaceFieldManager is the field manager name the agent's old
// read-modify-write namespace update recorded, before namespace metadata was
// switched to server-side apply. client-go derives it from the binary name
// (see cmd/fleetagent), so it is stable as long as that binary keeps its
// name.
const legacyNamespaceFieldManager = "fleetagent"

// migrateLegacyNamespaceManagedFields transfers ownership of the
// metadata.labels/metadata.annotations fields the legacy Update-based field
// manager still holds on ns into namespaceFieldOwner's Apply entry.
//
// Background: ForceOwnership on the first post-upgrade apply makes Fleet's
// SSA manager co-own any key it declares, but it does not strip the old
// "fleetagent" Update entry from managedFields. A field is only pruned once
// no manager owns it, so a key dropped from the bundle later stays behind
// forever on any namespace that predates the SSA switch, because the stale
// Update entry still owns it.
//
// This is deliberately scoped to metadata.labels/metadata.annotations rather
// than absorbing the whole legacy manager entry (as
// k8s.io/client-go/util/csaupgrade would): "fleetagent" is the agent's
// general-purpose field manager, so on some namespaces it may also own
// unrelated fields (for example finalizers) from other code paths. Blindly
// merging the entire entry into namespaceFieldOwner would make Fleet's
// namespace-metadata manager own -- and later prune -- fields it never
// declared and has no business managing.
//
// Returns nil if there is nothing to migrate (no stale entry, or the stale
// entry does not own any labels/annotations).
func migrateLegacyNamespaceManagedFields(ctx context.Context, c client.Client, ns *corev1.Namespace) error {
	patch, err := buildManagedFieldsMigrationPatch(ns)
	if err != nil {
		return fmt.Errorf("building managed fields migration patch for namespace %q: %w", ns.Name, err)
	}
	if patch == nil {
		return nil
	}

	if err := c.Patch(ctx, ns, client.RawPatch(types.JSONPatchType, patch)); err != nil {
		if apierrors.IsForbidden(err) {
			return fmt.Errorf("the deployment's service account is not allowed to patch namespace %q; "+
				"grant it 'patch' on this namespace (scoped via resourceNames) or remove "+
				"namespaceLabels/namespaceAnnotations: %w", ns.Name, err)
		}
		return err
	}

	return nil
}

// buildManagedFieldsMigrationPatch computes a JSON patch that moves ownership
// of the metadata.labels/metadata.annotations fields the legacy field manager
// (legacyNamespaceFieldManager) holds via an Update operation into
// namespaceFieldOwner's Apply entry, leaving any other fields the legacy
// manager owns untouched. It returns a nil patch (and nil error) if the legacy
// manager holds no such Update entry, or if that entry does not own any
// labels/annotations fields.
func buildManagedFieldsMigrationPatch(ns *corev1.Namespace) ([]byte, error) {
	entries := ns.ManagedFields

	legacyIdx := -1
	for i, e := range entries {
		if e.Manager == legacyNamespaceFieldManager && e.Operation == metav1.ManagedFieldsOperationUpdate && e.Subresource == "" {
			legacyIdx = i
			break
		}
	}
	if legacyIdx < 0 {
		return nil, nil
	}

	legacySet, err := decodeFieldsV1(entries[legacyIdx].FieldsV1)
	if err != nil {
		return nil, fmt.Errorf("decoding legacy managed fields: %w", err)
	}

	scoped := scopeToLabelsAndAnnotations(legacySet)
	if scoped.Empty() {
		return nil, nil
	}

	remaining := legacySet.Difference(scoped)

	ssaIdx := -1
	for i, e := range entries {
		if e.Manager == namespaceFieldOwner && e.Operation == metav1.ManagedFieldsOperationApply && e.Subresource == "" {
			ssaIdx = i
			break
		}
	}

	ssaSet := &fieldpath.Set{}
	if ssaIdx >= 0 {
		ssaSet, err = decodeFieldsV1(entries[ssaIdx].FieldsV1)
		if err != nil {
			return nil, fmt.Errorf("decoding ssa managed fields: %w", err)
		}
	}
	ssaSet = ssaSet.Union(scoped)

	newEntries := make([]metav1.ManagedFieldsEntry, 0, len(entries)+1)
	for i, e := range entries {
		switch i {
		case legacyIdx:
			if remaining.Empty() {
				continue // the legacy manager owns nothing else; drop the entry
			}
			raw, err := remaining.ToJSON()
			if err != nil {
				return nil, fmt.Errorf("encoding remaining legacy fields: %w", err)
			}
			e.FieldsV1 = &metav1.FieldsV1{}
			e.FieldsV1.SetRawBytes(raw)
		case ssaIdx:
			raw, err := ssaSet.ToJSON()
			if err != nil {
				return nil, fmt.Errorf("encoding ssa fields: %w", err)
			}
			e.FieldsV1 = &metav1.FieldsV1{}
			e.FieldsV1.SetRawBytes(raw)
		}
		newEntries = append(newEntries, e)
	}
	if ssaIdx < 0 {
		raw, err := ssaSet.ToJSON()
		if err != nil {
			return nil, fmt.Errorf("encoding ssa fields: %w", err)
		}
		fields := &metav1.FieldsV1{}
		fields.SetRawBytes(raw)
		newEntries = append(newEntries, metav1.ManagedFieldsEntry{
			Manager:    namespaceFieldOwner,
			Operation:  metav1.ManagedFieldsOperationApply,
			APIVersion: "v1",
			FieldsType: "FieldsV1",
			FieldsV1:   fields,
		})
	}

	jsonPatch := []map[string]any{
		{
			"op":    "replace",
			"path":  "/metadata/managedFields",
			"value": newEntries,
		},
		{
			// Use "replace" instead of "test" so the API server rejects a
			// concurrent modification with a 409 conflict rather than a
			// generic invalid-request error.
			"op":    "replace",
			"path":  "/metadata/resourceVersion",
			"value": ns.ResourceVersion,
		},
	}
	return json.Marshal(jsonPatch)
}

// scopeToLabelsAndAnnotations returns the subset of s whose paths are rooted
// at metadata.labels or metadata.annotations.
func scopeToLabelsAndAnnotations(s *fieldpath.Set) *fieldpath.Set {
	scoped := &fieldpath.Set{}
	s.Iterate(func(p fieldpath.Path) {
		if len(p) < 2 || p[0].FieldName == nil || p[1].FieldName == nil {
			return
		}
		if *p[0].FieldName != "metadata" {
			return
		}
		if *p[1].FieldName != "labels" && *p[1].FieldName != "annotations" {
			return
		}
		scoped.Insert(p)
	})
	return scoped
}

func decodeFieldsV1(f *metav1.FieldsV1) (*fieldpath.Set, error) {
	s := &fieldpath.Set{}
	if f == nil {
		return s, nil
	}
	if err := s.FromJSON(f.GetRawReader()); err != nil {
		return nil, err
	}
	return s, nil
}
