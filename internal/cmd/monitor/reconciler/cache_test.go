// Copyright (c) 2024-2026 SUSE LLC

package reconciler

import (
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestObjectCache_GetFromEmpty(t *testing.T) {
	cache := NewObjectCache()
	key := types.NamespacedName{Namespace: "ns", Name: "name"}
	_, exists := cache.Get(key)
	if exists {
		t.Error("expected false on Get from empty cache")
	}
}

func TestObjectCache_SetAndGet(t *testing.T) {
	cache := NewObjectCache()
	key := types.NamespacedName{Namespace: "ns", Name: "name"}
	bundle := &fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{Name: "name", Namespace: "ns"},
	}
	cache.Set(key, bundle)

	got, exists := cache.Get(key)
	if !exists {
		t.Fatal("expected true on Get after Set")
	}
	if got.GetName() != bundle.Name || got.GetNamespace() != bundle.Namespace {
		t.Errorf("got %s/%s, want %s/%s", got.GetNamespace(), got.GetName(), bundle.Namespace, bundle.Name)
	}
}

func TestObjectCache_Delete(t *testing.T) {
	cache := NewObjectCache()
	key := types.NamespacedName{Namespace: "ns", Name: "name"}
	bundle := &fleet.Bundle{ObjectMeta: metav1.ObjectMeta{Name: "name", Namespace: "ns"}}
	cache.Set(key, bundle)

	cache.Delete(key)

	_, exists := cache.Get(key)
	if exists {
		t.Error("expected false on Get after Delete")
	}
}

func TestObjectCache_DeleteMissingKey(t *testing.T) {
	cache := NewObjectCache()
	key := types.NamespacedName{Namespace: "ns", Name: "does-not-exist"}
	// Should not panic
	cache.Delete(key)
}

func TestObjectCache_OverwriteExisting(t *testing.T) {
	cache := NewObjectCache()
	key := types.NamespacedName{Namespace: "ns", Name: "name"}

	original := &fleet.Bundle{ObjectMeta: metav1.ObjectMeta{Name: "name", Namespace: "ns", ResourceVersion: "1"}}
	updated := &fleet.Bundle{ObjectMeta: metav1.ObjectMeta{Name: "name", Namespace: "ns", ResourceVersion: "2"}}

	cache.Set(key, original)
	cache.Set(key, updated)

	got, exists := cache.Get(key)
	if !exists {
		t.Fatal("expected entry to exist")
	}
	if got.GetResourceVersion() != "2" {
		t.Errorf("got ResourceVersion %q, want %q", got.GetResourceVersion(), "2")
	}
}

func TestObjectCache_MultipleKeys(t *testing.T) {
	cache := NewObjectCache()
	keys := []types.NamespacedName{
		{Namespace: "ns", Name: "a"},
		{Namespace: "ns", Name: "b"},
		{Namespace: "other-ns", Name: "a"},
	}

	for _, k := range keys {
		bundle := &fleet.Bundle{ObjectMeta: metav1.ObjectMeta{Name: k.Name, Namespace: k.Namespace}}
		cache.Set(k, bundle)
	}

	for _, k := range keys {
		got, exists := cache.Get(k)
		if !exists {
			t.Errorf("expected key %v to exist", k)
			continue
		}
		if got.GetName() != k.Name || got.GetNamespace() != k.Namespace {
			t.Errorf("got %s/%s, want %s/%s", got.GetNamespace(), got.GetName(), k.Namespace, k.Name)
		}
	}
}

func TestObjectCache_IndependentKeys(t *testing.T) {
	cache := NewObjectCache()
	keyA := types.NamespacedName{Namespace: "ns", Name: "a"}
	keyB := types.NamespacedName{Namespace: "ns", Name: "b"}

	cache.Set(keyA, &fleet.Bundle{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "ns"}})
	cache.Delete(keyA)

	// Deleting A should not affect B (which was never set)
	_, exists := cache.Get(keyB)
	if exists {
		t.Error("expected B to not exist after deleting A")
	}
}
