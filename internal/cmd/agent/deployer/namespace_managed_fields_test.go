package deployer

import (
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/structured-merge-diff/v6/fieldpath"
)

func fieldsV1FromPaths(t *testing.T, paths ...fieldpath.Path) *metav1.FieldsV1 {
	t.Helper()
	s := fieldpath.NewSet(paths...)
	raw, err := s.ToJSON()
	if err != nil {
		t.Fatalf("encode set: %v", err)
	}
	f := &metav1.FieldsV1{}
	f.SetRawBytes(raw)
	return f
}

func pe(name string) fieldpath.PathElement { return fieldpath.FieldNameElement(name) }

func TestBuildManagedFieldsMigrationPatch_NoLegacyEntry(t *testing.T) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			ManagedFields: []metav1.ManagedFieldsEntry{
				{Manager: "someone-else", Operation: metav1.ManagedFieldsOperationUpdate},
			},
		},
	}

	patch, err := buildManagedFieldsMigrationPatch(ns)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if patch != nil {
		t.Errorf("expected nil patch when there is no legacy entry, got %s", patch)
	}
}

func TestBuildManagedFieldsMigrationPatch_LegacyEntryWithoutLabelsOrAnnotations(t *testing.T) {
	// The legacy manager owns something unrelated (e.g. finalizers); nothing
	// under metadata.labels/annotations, so there is nothing to migrate.
	fields := fieldsV1FromPaths(t, fieldpath.Path{pe("metadata"), pe("finalizers")})
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		ResourceVersion: "1",
		ManagedFields: []metav1.ManagedFieldsEntry{
			{Manager: legacyNamespaceFieldManager, Operation: metav1.ManagedFieldsOperationUpdate, FieldsV1: fields},
		},
	}}

	patch, err := buildManagedFieldsMigrationPatch(ns)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if patch != nil {
		t.Errorf("expected nil patch, got %s", patch)
	}
}

func TestBuildManagedFieldsMigrationPatch_ScopesToLabelsAndAnnotationsOnly(t *testing.T) {
	// The legacy manager owns an annotation, a label, AND an unrelated field
	// (finalizers). Only the annotation/label paths must move to the SSA
	// manager; finalizers must stay with the legacy manager untouched.
	fields := fieldsV1FromPaths(
		t,
		fieldpath.Path{pe("metadata"), pe("annotations"), pe("fleet-a")},
		fieldpath.Path{pe("metadata"), pe("labels"), pe("team")},
		fieldpath.Path{pe("metadata"), pe("finalizers")},
	)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name:            "test",
		ResourceVersion: "42",
		ManagedFields: []metav1.ManagedFieldsEntry{
			{Manager: legacyNamespaceFieldManager, Operation: metav1.ManagedFieldsOperationUpdate, APIVersion: "v1", FieldsV1: fields},
		},
	}}

	patch, err := buildManagedFieldsMigrationPatch(ns)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if patch == nil {
		t.Fatalf("expected a non-nil patch")
	}

	var ops []map[string]json.RawMessage
	if err := json.Unmarshal(patch, &ops); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 patch ops, got %d: %s", len(ops), patch)
	}

	var newEntries []metav1.ManagedFieldsEntry
	if err := json.Unmarshal(ops[0]["value"], &newEntries); err != nil {
		t.Fatalf("unmarshal managedFields value: %v", err)
	}

	var legacyEntry, ssaEntry *metav1.ManagedFieldsEntry
	for i := range newEntries {
		switch newEntries[i].Manager {
		case legacyNamespaceFieldManager:
			legacyEntry = &newEntries[i]
		case namespaceFieldOwner:
			ssaEntry = &newEntries[i]
		}
	}

	if legacyEntry == nil {
		t.Fatalf("legacy entry should still exist (it still owns finalizers): %+v", newEntries)
	}
	legacySet, err := decodeFieldsV1(legacyEntry.FieldsV1)
	if err != nil {
		t.Fatalf("decode legacy fields: %v", err)
	}
	if legacySet.Has(fieldpath.Path{pe("metadata"), pe("annotations"), pe("fleet-a")}) {
		t.Errorf("legacy entry should no longer own the migrated annotation")
	}
	if legacySet.Has(fieldpath.Path{pe("metadata"), pe("labels"), pe("team")}) {
		t.Errorf("legacy entry should no longer own the migrated label")
	}
	if !legacySet.Has(fieldpath.Path{pe("metadata"), pe("finalizers")}) {
		t.Errorf("legacy entry must keep ownership of unrelated fields like finalizers")
	}

	if ssaEntry == nil {
		t.Fatalf("expected a new/updated SSA manager entry: %+v", newEntries)
	}
	if ssaEntry.Operation != metav1.ManagedFieldsOperationApply {
		t.Errorf("expected SSA entry operation Apply, got %v", ssaEntry.Operation)
	}
	ssaSet, err := decodeFieldsV1(ssaEntry.FieldsV1)
	if err != nil {
		t.Fatalf("decode ssa fields: %v", err)
	}
	if !ssaSet.Has(fieldpath.Path{pe("metadata"), pe("annotations"), pe("fleet-a")}) {
		t.Errorf("SSA entry should now own the migrated annotation")
	}
	if !ssaSet.Has(fieldpath.Path{pe("metadata"), pe("labels"), pe("team")}) {
		t.Errorf("SSA entry should now own the migrated label")
	}
	if ssaSet.Has(fieldpath.Path{pe("metadata"), pe("finalizers")}) {
		t.Errorf("SSA entry must never absorb unrelated fields like finalizers")
	}
}

func TestBuildManagedFieldsMigrationPatch_DropsLegacyEntryWhenFullyMigrated(t *testing.T) {
	// The legacy manager owns only labels/annotations; once migrated, its
	// entry should be removed entirely rather than left behind empty.
	fields := fieldsV1FromPaths(
		t,
		fieldpath.Path{pe("metadata"), pe("annotations"), pe("fleet-a")},
	)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		ResourceVersion: "1",
		ManagedFields: []metav1.ManagedFieldsEntry{
			{Manager: legacyNamespaceFieldManager, Operation: metav1.ManagedFieldsOperationUpdate, APIVersion: "v1", FieldsV1: fields},
		},
	}}

	patch, err := buildManagedFieldsMigrationPatch(ns)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if patch == nil {
		t.Fatalf("expected a non-nil patch")
	}

	var ops []map[string]json.RawMessage
	if err := json.Unmarshal(patch, &ops); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	var newEntries []metav1.ManagedFieldsEntry
	if err := json.Unmarshal(ops[0]["value"], &newEntries); err != nil {
		t.Fatalf("unmarshal managedFields value: %v", err)
	}

	for _, e := range newEntries {
		if e.Manager == legacyNamespaceFieldManager {
			t.Errorf("legacy entry should have been dropped entirely, got %+v", e)
		}
	}
	if len(newEntries) != 1 || newEntries[0].Manager != namespaceFieldOwner {
		t.Fatalf("expected exactly one SSA entry, got %+v", newEntries)
	}
}
