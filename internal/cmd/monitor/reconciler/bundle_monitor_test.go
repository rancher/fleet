// Copyright (c) 2024-2026 SUSE LLC

package reconciler

import (
	"context"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func newBundleTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(fleet.AddToScheme(s))
	return s
}

// newBundleReconciler creates a BundleMonitorReconciler with a fresh cache and the given objects.
func newBundleReconciler(t *testing.T, sch *runtime.Scheme, filter *ResourceFilter, objs ...fleet.Bundle) (*BundleMonitorReconciler, func()) {
	t.Helper()
	builder := fake.NewClientBuilder().WithScheme(sch)
	for i := range objs {
		builder = builder.WithObjects(&objs[i])
	}
	c := builder.Build()
	r := &BundleMonitorReconciler{
		Client:         c,
		Scheme:         sch,
		cache:          NewObjectCache(),
		ResourceFilter: filter,
	}
	reset := func() { globalStatsTracker.Reset() }
	globalStatsTracker.Reset()
	return r, reset
}

func TestBundleMonitorReconciler_ResourceFilterSkip(t *testing.T) {
	sch := newBundleTestScheme(t)
	filter := &ResourceFilter{NamespacePattern: "fleet-.*"}
	_ = filter.Compile()

	r, cleanup := newBundleReconciler(t, sch, filter)
	defer cleanup()

	ctx := context.Background()
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "my-bundle"}}
	result, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Errorf("expected empty result, got %v", result)
	}
	// No stats should have been recorded for a filtered resource
	if len(globalStatsTracker.stats) != 0 {
		t.Errorf("expected no stats recorded for filtered resource, got %d entries", len(globalStatsTracker.stats))
	}
}

func TestBundleMonitorReconciler_NotFound(t *testing.T) {
	sch := newBundleTestScheme(t)
	r, cleanup := newBundleReconciler(t, sch, nil)
	defer cleanup()

	ctx := context.Background()
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "missing"}}
	result, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Errorf("expected empty result, got %v", result)
	}

	key := ResourceKey{ResourceType: "Bundle", Namespace: "ns", Name: "missing"}
	stats := globalStatsTracker.stats[key]
	if stats == nil || stats.Counts[EventTypeNotFound] != 1 {
		t.Error("expected NotFound event to be recorded")
	}
}

func TestBundleMonitorReconciler_FirstObservation(t *testing.T) {
	sch := newBundleTestScheme(t)
	bundle := fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "my-bundle",
			Namespace:       "ns",
			ResourceVersion: "1",
			Generation:      1,
		},
	}
	r, cleanup := newBundleReconciler(t, sch, nil, bundle)
	defer cleanup()

	ctx := context.Background()
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "my-bundle"}}
	result, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Errorf("expected empty result, got %v", result)
	}

	// Object should be cached after first observation
	_, exists := r.cache.Get(req.NamespacedName)
	if !exists {
		t.Error("expected bundle to be in cache after first observation")
	}

	key := ResourceKey{ResourceType: "Bundle", Namespace: "ns", Name: "my-bundle"}
	stats := globalStatsTracker.stats[key]
	if stats == nil || stats.Counts[EventTypeCreate] != 1 {
		t.Error("expected Create event to be recorded on first observation")
	}
}

func TestBundleMonitorReconciler_Deletion(t *testing.T) {
	sch := newBundleTestScheme(t)
	now := metav1.Now()
	bundle := fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "my-bundle",
			Namespace:         "ns",
			ResourceVersion:   "1",
			DeletionTimestamp: &now,
			Finalizers:        []string{"fleet.cattle.io/bundle-finalizer"},
		},
	}
	r, cleanup := newBundleReconciler(t, sch, nil, bundle)
	defer cleanup()

	// Pre-populate cache to simulate a prior observation
	cacheKey := types.NamespacedName{Namespace: "ns", Name: "my-bundle"}
	r.cache.Set(cacheKey, bundle.DeepCopy())

	ctx := context.Background()
	req := reconcile.Request{NamespacedName: cacheKey}
	_, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Cache entry should be removed on deletion
	_, exists := r.cache.Get(cacheKey)
	if exists {
		t.Error("expected bundle to be removed from cache after deletion")
	}

	statsKey := ResourceKey{ResourceType: "Bundle", Namespace: "ns", Name: "my-bundle"}
	stats := globalStatsTracker.stats[statsKey]
	if stats == nil || stats.Counts[EventTypeDeletion] != 1 {
		t.Error("expected Deletion event to be recorded")
	}
}

func TestBundleMonitorReconciler_GenerationChange(t *testing.T) {
	sch := newBundleTestScheme(t)
	bundle := fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "my-bundle",
			Namespace:       "ns",
			ResourceVersion: "2",
			Generation:      2,
		},
	}
	r, cleanup := newBundleReconciler(t, sch, nil, bundle)
	defer cleanup()

	// Put old version (generation=1) in cache
	cacheKey := types.NamespacedName{Namespace: "ns", Name: "my-bundle"}
	oldBundle := bundle.DeepCopy()
	oldBundle.Generation = 1
	r.cache.Set(cacheKey, oldBundle)

	ctx := context.Background()
	req := reconcile.Request{NamespacedName: cacheKey}
	_, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Cache should be updated to the new generation
	cached, exists := r.cache.Get(cacheKey)
	if !exists {
		t.Fatal("expected bundle to remain in cache after update")
	}
	if cached.(*fleet.Bundle).Generation != 2 {
		t.Errorf("expected generation 2 in cache, got %d", cached.(*fleet.Bundle).Generation)
	}

	statsKey := ResourceKey{ResourceType: "Bundle", Namespace: "ns", Name: "my-bundle"}
	stats := globalStatsTracker.stats[statsKey]
	if stats == nil || stats.Counts[EventTypeGenerationChange] != 1 {
		t.Errorf("expected GenerationChange event to be recorded, stats = %+v", stats)
	}
}

func TestBundleMonitorReconciler_StatusChange(t *testing.T) {
	sch := newBundleTestScheme(t)
	bundle := fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "my-bundle",
			Namespace:       "ns",
			ResourceVersion: "2",
			Generation:      1,
		},
		Status: fleet.BundleStatus{
			Summary: fleet.BundleSummary{Ready: 3},
		},
	}
	r, cleanup := newBundleReconciler(t, sch, nil, bundle)
	defer cleanup()

	cacheKey := types.NamespacedName{Namespace: "ns", Name: "my-bundle"}
	oldBundle := bundle.DeepCopy()
	oldBundle.Status.Summary.Ready = 1
	r.cache.Set(cacheKey, oldBundle)

	ctx := context.Background()
	req := reconcile.Request{NamespacedName: cacheKey}
	_, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	statsKey := ResourceKey{ResourceType: "Bundle", Namespace: "ns", Name: "my-bundle"}
	stats := globalStatsTracker.stats[statsKey]
	if stats == nil || stats.Counts[EventTypeStatusChange] != 1 {
		t.Errorf("expected StatusChange event to be recorded, stats = %+v", stats)
	}
}

func TestBundleMonitorReconciler_AnnotationChange(t *testing.T) {
	sch := newBundleTestScheme(t)
	bundle := fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "my-bundle",
			Namespace:       "ns",
			ResourceVersion: "2",
			Generation:      1,
			Annotations:     map[string]string{"key": "new-value"},
		},
	}
	r, cleanup := newBundleReconciler(t, sch, nil, bundle)
	defer cleanup()

	cacheKey := types.NamespacedName{Namespace: "ns", Name: "my-bundle"}
	oldBundle := bundle.DeepCopy()
	oldBundle.Annotations["key"] = "old-value"
	r.cache.Set(cacheKey, oldBundle)

	ctx := context.Background()
	req := reconcile.Request{NamespacedName: cacheKey}
	_, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	statsKey := ResourceKey{ResourceType: "Bundle", Namespace: "ns", Name: "my-bundle"}
	stats := globalStatsTracker.stats[statsKey]
	if stats == nil || stats.Counts[EventTypeAnnotationChange] != 1 {
		t.Errorf("expected AnnotationChange event to be recorded, stats = %+v", stats)
	}
}

func TestBundleMonitorReconciler_LabelChange(t *testing.T) {
	sch := newBundleTestScheme(t)
	bundle := fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "my-bundle",
			Namespace:       "ns",
			ResourceVersion: "2",
			Generation:      1,
			Labels:          map[string]string{"env": "production"},
		},
	}
	r, cleanup := newBundleReconciler(t, sch, nil, bundle)
	defer cleanup()

	cacheKey := types.NamespacedName{Namespace: "ns", Name: "my-bundle"}
	oldBundle := bundle.DeepCopy()
	oldBundle.Labels["env"] = "staging"
	r.cache.Set(cacheKey, oldBundle)

	ctx := context.Background()
	req := reconcile.Request{NamespacedName: cacheKey}
	_, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	statsKey := ResourceKey{ResourceType: "Bundle", Namespace: "ns", Name: "my-bundle"}
	stats := globalStatsTracker.stats[statsKey]
	if stats == nil || stats.Counts[EventTypeLabelChange] != 1 {
		t.Errorf("expected LabelChange event to be recorded, stats = %+v", stats)
	}
}

func TestBundleMonitorReconciler_NoChange(t *testing.T) {
	sch := newBundleTestScheme(t)
	bundle := fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "my-bundle",
			Namespace:       "ns",
			ResourceVersion: "1",
			Generation:      1,
		},
	}
	r, cleanup := newBundleReconciler(t, sch, nil, bundle)
	defer cleanup()

	cacheKey := types.NamespacedName{Namespace: "ns", Name: "my-bundle"}
	r.cache.Set(cacheKey, bundle.DeepCopy())

	ctx := context.Background()
	req := reconcile.Request{NamespacedName: cacheKey}
	_, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No change events expected
	statsKey := ResourceKey{ResourceType: "Bundle", Namespace: "ns", Name: "my-bundle"}
	stats := globalStatsTracker.stats[statsKey]
	if stats != nil && stats.Total > 0 {
		t.Errorf("expected no events recorded for unchanged bundle, got total=%d", stats.Total)
	}
}
