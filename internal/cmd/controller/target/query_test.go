package target

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func newScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = fleet.AddToScheme(scheme)
	return scheme
}

func makeCGForQuery(name, namespace, rv string, selector *metav1.LabelSelector) *fleet.ClusterGroup {
	return &fleet.ClusterGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			ResourceVersion: rv,
		},
		Spec: fleet.ClusterGroupSpec{
			Selector: selector,
		},
	}
}

func TestClusterGroupsForCluster_Matching(t *testing.T) {
	const ns = "fleet-default"

	testCases := []struct {
		name          string
		cgs           []runtime.Object
		clusterLabels map[string]string
		expectedNames []string
	}{
		{
			name: "matching cluster group returned",
			cgs: []runtime.Object{
				makeCGForQuery("prod-cg", ns, "1", &metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "prod"},
				}),
			},
			clusterLabels: map[string]string{"env": "prod"},
			expectedNames: []string{"prod-cg"},
		},
		{
			name: "non-matching cluster group excluded",
			cgs: []runtime.Object{
				makeCGForQuery("prod-cg", ns, "1", &metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "prod"},
				}),
			},
			clusterLabels: map[string]string{"env": "staging"},
			expectedNames: []string{},
		},
		{
			name: "nil selector cluster group excluded",
			cgs: []runtime.Object{
				makeCGForQuery("no-selector-cg", ns, "1", nil),
			},
			clusterLabels: map[string]string{"env": "prod"},
			expectedNames: []string{},
		},
		{
			name: "invalid selector skipped",
			cgs: []runtime.Object{
				makeCGForQuery("bad-cg", ns, "1", &metav1.LabelSelector{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{Key: "env", Operator: "InvalidOp", Values: []string{"prod"}},
					},
				}),
			},
			clusterLabels: map[string]string{"env": "prod"},
			expectedNames: []string{},
		},
		{
			name: "multiple cgs, only matching returned",
			cgs: []runtime.Object{
				makeCGForQuery("prod-cg", ns, "1", &metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "prod"},
				}),
				makeCGForQuery("staging-cg", ns, "1", &metav1.LabelSelector{
					MatchLabels: map[string]string{"env": "staging"},
				}),
			},
			clusterLabels: map[string]string{"env": "prod"},
			expectedNames: []string{"prod-cg"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().WithScheme(newScheme()).WithRuntimeObjects(tc.cgs...).Build()
			manager := New(fakeClient, fakeClient)

			cluster := &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: ns,
					Labels:    tc.clusterLabels,
				},
			}

			result, err := manager.clusterGroupsForCluster(context.Background(), cluster)
			assert.NoError(t, err)

			names := make([]string, len(result))
			for i, cg := range result {
				names[i] = cg.Name
			}
			if len(tc.expectedNames) == 0 {
				assert.Empty(t, names)
			} else {
				assert.Equal(t, tc.expectedNames, names)
			}
		})
	}
}

func TestClusterGroupsForCluster_SelectorCachedAfterFirstCall(t *testing.T) {
	const ns = "fleet-default"

	cg := makeCGForQuery("prod-cg", ns, "42", &metav1.LabelSelector{
		MatchLabels: map[string]string{"env": "prod"},
	})

	fakeClient := fake.NewClientBuilder().WithScheme(newScheme()).WithRuntimeObjects(cg).Build()
	manager := New(fakeClient, fakeClient)

	cluster := &fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: ns,
			Labels:    map[string]string{"env": "prod"},
		},
	}

	result1, err := manager.clusterGroupsForCluster(context.Background(), cluster)
	assert.NoError(t, err)
	assert.Len(t, result1, 1)

	cacheKey := ns + "/prod-cg@42"
	cached, ok := manager.selectorCache.Load(cacheKey)
	assert.True(t, ok, "selector should be in selectorCache after first call")
	assert.Implements(t, (*labels.Selector)(nil), cached)

	result2, err := manager.clusterGroupsForCluster(context.Background(), cluster)
	assert.NoError(t, err)
	assert.Len(t, result2, 1)
}

func TestClusterGroupsForCluster_InvalidSelectorNotCached(t *testing.T) {
	const ns = "fleet-default"

	cg := makeCGForQuery("bad-cg", ns, "1", &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{Key: "env", Operator: "InvalidOp", Values: []string{"prod"}},
		},
	})

	fakeClient := fake.NewClientBuilder().WithScheme(newScheme()).WithRuntimeObjects(cg).Build()
	manager := New(fakeClient, fakeClient)

	cluster := &fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: ns,
			Labels:    map[string]string{"env": "prod"},
		},
	}

	result, err := manager.clusterGroupsForCluster(context.Background(), cluster)
	assert.NoError(t, err)
	assert.Empty(t, result)

	_, cached := manager.selectorCache.Load(ns + "/bad-cg@1")
	assert.False(t, cached, "invalid selector must not be stored in selectorCache")
}

func TestClusterGroupsForCluster_NewResourceVersionCreatesNewCacheEntry(t *testing.T) {
	// When a ClusterGroup is updated (ResourceVersion bumped), a new cache entry
	// must be created so the updated selector is compiled rather than served stale.
	const ns = "fleet-default"

	cg := makeCGForQuery("prod-cg", ns, "1", &metav1.LabelSelector{
		MatchLabels: map[string]string{"env": "prod"},
	})

	cluster := &fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: ns,
			Labels:    map[string]string{"env": "prod"},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(newScheme()).WithRuntimeObjects(cg).Build()
	manager := New(fakeClient, fakeClient)

	// First call — populates cache with rv=1 entry.
	_, err := manager.clusterGroupsForCluster(context.Background(), cluster)
	assert.NoError(t, err)
	_, v1Cached := manager.selectorCache.Load(ns + "/prod-cg@1")
	assert.True(t, v1Cached, "entry for rv=1 should be cached after first call")

	// Simulate a ClusterGroup update by bumping the ResourceVersion.
	cg.ResourceVersion = "2"
	err = fakeClient.Update(context.Background(), cg)
	assert.NoError(t, err)

	// Second call — must create a new cache entry for rv=2.
	_, err = manager.clusterGroupsForCluster(context.Background(), cluster)
	assert.NoError(t, err)
	_, v2Cached := manager.selectorCache.Load(ns + "/prod-cg@2")
	assert.True(t, v2Cached, "entry for rv=2 should be cached after update")
}
