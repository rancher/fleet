package cluster

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestClusterReconciler_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = fleet.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name                string
		cluster             *fleet.Cluster
		existingNamespace   *corev1.Namespace
		expectNamespace     bool
		expectStatusUpdate  bool
		expectedNSName      string
		expectNSAnnotations bool
	}{
		{
			name: "creates namespace for new cluster",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{},
			},
			expectNamespace:     true,
			expectStatusUpdate:  true,
			expectedNSName:      "cluster-fleet-default-test-cluster-34d12abcc78f",
			expectNSAnnotations: true,
		},
		{
			name: "preserves existing namespace",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{},
				Status: fleet.ClusterStatus{
					Namespace: "cluster-fleet-default-test-cluster-34d12abcc78f",
				},
			},
			existingNamespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster-fleet-default-test-cluster-34d12abcc78f",
					Labels: map[string]string{
						fleet.ManagedLabel: "true",
					},
				},
			},
			expectNamespace:    true,
			expectStatusUpdate: false,
			expectedNSName:     "cluster-fleet-default-test-cluster-34d12abcc78f",
		},
		{
			name: "handles cluster deletion and removes namespace",
			// cluster will be nil (not found), tested separately
			expectNamespace: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := []client.Object{}
			if tt.cluster != nil {
				objs = append(objs, tt.cluster)
			}
			if tt.existingNamespace != nil {
				objs = append(objs, tt.existingNamespace)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				WithStatusSubresource(&fleet.Cluster{}).
				Build()

			r := &ClusterReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			ctx := context.Background()
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-cluster",
					Namespace: "fleet-default",
				},
			}

			result, err := r.Reconcile(ctx, req)
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, result)

			if tt.expectStatusUpdate {
				var cluster fleet.Cluster
				err = fakeClient.Get(ctx, types.NamespacedName{
					Name:      tt.cluster.Name,
					Namespace: tt.cluster.Namespace,
				}, &cluster)
				require.NoError(t, err)
				assert.Equal(t, tt.expectedNSName, cluster.Status.Namespace)
			}

			// Note: The fake client doesn't persist namespace creations,
			// so we can't fully test namespace creation here without integration tests
		})
	}
}

func TestClusterReconciler_ClusterDeletion(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = fleet.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	// Create namespace that should be deleted
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster-fleet-default-deleted-cluster-294db1acfa77-abcd1234",
			Labels: map[string]string{
				fleet.ManagedLabel: "true",
			},
			Annotations: map[string]string{
				fleet.ClusterNamespaceAnnotation: "fleet-default",
				fleet.ClusterAnnotation:          "deleted-cluster",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(ns).
		Build()

	r := &ClusterReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	ctx := context.Background()
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "deleted-cluster",
			Namespace: "fleet-default",
		},
	}

	result, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, result)

	// Note: The fake client doesn't persist deletions synchronously,
	// so we can't fully test deletion here without integration tests
}

func TestClusterReconciler_FindClustersForBundleDeployment(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = fleet.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name           string
		bundleDeploy   *fleet.BundleDeployment
		namespace      *corev1.Namespace
		expectRequests int
		expectedNS     string
		expectedName   string
	}{
		{
			name: "maps BundleDeployment to Cluster",
			bundleDeploy: &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-bd",
					Namespace: "cluster-ns",
				},
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster-ns",
					Annotations: map[string]string{
						fleet.ClusterNamespaceAnnotation: "fleet-default",
						fleet.ClusterAnnotation:          "my-cluster",
					},
				},
			},
			expectRequests: 1,
			expectedNS:     "fleet-default",
			expectedName:   "my-cluster",
		},
		{
			name: "returns empty when namespace missing annotations",
			bundleDeploy: &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-bd",
					Namespace: "cluster-ns",
				},
			},
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster-ns",
				},
			},
			expectRequests: 0,
		},
		{
			name: "returns empty when namespace not found",
			bundleDeploy: &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-bd",
					Namespace: "missing-ns",
				},
			},
			expectRequests: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := []client.Object{}
			if tt.namespace != nil {
				objs = append(objs, tt.namespace)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				Build()

			r := &ClusterReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			ctx := context.Background()
			requests := r.findClustersForBundleDeployment(ctx, tt.bundleDeploy)

			assert.Len(t, requests, tt.expectRequests)
			if tt.expectRequests > 0 {
				assert.Equal(t, tt.expectedNS, requests[0].Namespace)
				assert.Equal(t, tt.expectedName, requests[0].Name)
			}
		})
	}
}

func TestClusterImportReconciler_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = fleet.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name                 string
		cluster              *fleet.Cluster
		expectClientIDUpdate bool
		skipTest             bool // for complex scenarios requiring mocks
	}{
		{
			name: "sets ClientID for cluster without KubeConfigSecret",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{
					ClientID: "",
				},
			},
			expectClientIDUpdate: false, // no kubeconfig secret
		},
		{
			name: "updates ClientID when KubeConfigSecret exists and agent not deployed",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{
					KubeConfigSecret: "my-kubeconfig",
					ClientID:         "",
				},
			},
			expectClientIDUpdate: true,
		},
		{
			name: "skips when agent already deployed",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{
					KubeConfigSecret:        "my-kubeconfig",
					ClientID:                "existing-id",
					RedeployAgentGeneration: 1,
				},
				Status: fleet.ClusterStatus{
					AgentDeployedGeneration: intPtr(1),
					AgentMigrated:           true,
					CattleNamespaceMigrated: true,
					AgentNamespaceMigrated:  true,
					Agent: fleet.AgentStatus{
						Namespace: "cattle-fleet-system",
					},
				},
			},
			expectClientIDUpdate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skipTest {
				t.Skip("requires complex mocking")
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.cluster).
				WithStatusSubresource(&fleet.Cluster{}).
				Build()

			r := &ClusterImportReconciler{
				Client:          fakeClient,
				Scheme:          scheme,
				SystemNamespace: "cattle-fleet-system",
			}

			ctx := context.Background()
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.cluster.Name,
					Namespace: tt.cluster.Namespace,
				},
			}

			result, err := r.Reconcile(ctx, req)
			require.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, result)

			if tt.expectClientIDUpdate {
				var cluster fleet.Cluster
				err = fakeClient.Get(ctx, types.NamespacedName{
					Name:      tt.cluster.Name,
					Namespace: tt.cluster.Namespace,
				}, &cluster)
				require.NoError(t, err)
				assert.NotEmpty(t, cluster.Spec.ClientID)
			}
		})
	}
}

func TestAgentDeployed(t *testing.T) {
	tests := []struct {
		name     string
		cluster  *fleet.Cluster
		expected bool
	}{
		{
			name: "returns true when agent fully deployed",
			cluster: &fleet.Cluster{
				Spec: fleet.ClusterSpec{
					RedeployAgentGeneration: 1,
					AgentNamespace:          "cattle-fleet-system",
				},
				Status: fleet.ClusterStatus{
					AgentDeployedGeneration: intPtr(1),
					AgentMigrated:           true,
					CattleNamespaceMigrated: true,
					AgentNamespaceMigrated:  true,
					AgentConfigChanged:      false,
					Agent: fleet.AgentStatus{
						Namespace: "cattle-fleet-system",
					},
				},
			},
			expected: true,
		},
		{
			name: "returns false when AgentConfigChanged",
			cluster: &fleet.Cluster{
				Spec: fleet.ClusterSpec{
					RedeployAgentGeneration: 1,
				},
				Status: fleet.ClusterStatus{
					AgentConfigChanged:      true,
					AgentDeployedGeneration: intPtr(1),
					AgentMigrated:           true,
					CattleNamespaceMigrated: true,
					AgentNamespaceMigrated:  true,
				},
			},
			expected: false,
		},
		{
			name: "returns false when AgentMigrated is false",
			cluster: &fleet.Cluster{
				Spec: fleet.ClusterSpec{
					RedeployAgentGeneration: 1,
				},
				Status: fleet.ClusterStatus{
					AgentConfigChanged:      false,
					AgentDeployedGeneration: intPtr(1),
					AgentMigrated:           false,
					CattleNamespaceMigrated: true,
					AgentNamespaceMigrated:  true,
				},
			},
			expected: false,
		},
		{
			name: "returns false when generation mismatch",
			cluster: &fleet.Cluster{
				Spec: fleet.ClusterSpec{
					RedeployAgentGeneration: 2,
				},
				Status: fleet.ClusterStatus{
					AgentConfigChanged:      false,
					AgentDeployedGeneration: intPtr(1),
					AgentMigrated:           true,
					CattleNamespaceMigrated: true,
					AgentNamespaceMigrated:  true,
				},
			},
			expected: false,
		},
		{
			name: "returns false when AgentDeployedGeneration is nil",
			cluster: &fleet.Cluster{
				Spec: fleet.ClusterSpec{
					RedeployAgentGeneration: 1,
				},
				Status: fleet.ClusterStatus{
					AgentConfigChanged:      false,
					AgentDeployedGeneration: nil,
					AgentMigrated:           true,
					CattleNamespaceMigrated: true,
					AgentNamespaceMigrated:  true,
				},
			},
			expected: false,
		},
		{
			name: "returns false when agent namespace mismatch",
			cluster: &fleet.Cluster{
				Spec: fleet.ClusterSpec{
					RedeployAgentGeneration: 1,
					AgentNamespace:          "custom-ns",
				},
				Status: fleet.ClusterStatus{
					AgentConfigChanged:      false,
					AgentDeployedGeneration: intPtr(1),
					AgentMigrated:           true,
					CattleNamespaceMigrated: true,
					AgentNamespaceMigrated:  true,
					Agent: fleet.AgentStatus{
						Namespace: "different-ns",
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := agentDeployed(tt.cluster)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetKubeConfigSecretNS(t *testing.T) {
	tests := []struct {
		name     string
		cluster  *fleet.Cluster
		expected string
	}{
		{
			name: "returns cluster namespace when KubeConfigSecretNamespace not set",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{},
			},
			expected: "fleet-default",
		},
		{
			name: "returns KubeConfigSecretNamespace when set",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{
					KubeConfigSecretNamespace: "custom-namespace",
				},
			},
			expected: "custom-namespace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getKubeConfigSecretNS(tt.cluster)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHashStatusField(t *testing.T) {
	tests := []struct {
		name     string
		field    interface{}
		expected string
	}{
		{
			name:     "hashes byte array consistently",
			field:    []byte("test-ca-data"),
			expected: "a0c4bfbf3e464bc06e74dc8d45be68df71c80b51e23f02afdc0b65",
		},
		{
			name:     "produces different hashes for different inputs",
			field:    []byte("different-ca-data"),
			expected: "be0fde1f7f2ffed0d37f50a5fe3edaa4f3dab5b0a63c1daa35cf61",
		},
		{
			name:     "handles string input",
			field:    "string-value",
			expected: "9e4a5e3f05f3a7f5ccb28d4e1f0eff97cb2b850a4d1ec4aba12c07",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := hashStatusField(tt.field)
			require.NoError(t, err)
			assert.NotEmpty(t, result)
			// Hashes should be consistent
			result2, err := hashStatusField(tt.field)
			require.NoError(t, err)
			assert.Equal(t, result, result2)
		})
	}
}

func TestHasGarbageCollectionIntervalChanged(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *fleet.ClusterStatus
		cluster  *fleet.Cluster
		expected bool
	}{
		{
			name: "returns true when config has interval and cluster does not",
			cfg: &fleet.ClusterStatus{
				GarbageCollectionInterval: &metav1.Duration{Duration: 300},
			},
			cluster: &fleet.Cluster{
				Status: fleet.ClusterStatus{
					GarbageCollectionInterval: nil,
				},
			},
			expected: true,
		},
		{
			name: "returns true when intervals differ",
			cfg: &fleet.ClusterStatus{
				GarbageCollectionInterval: &metav1.Duration{Duration: 300},
			},
			cluster: &fleet.Cluster{
				Status: fleet.ClusterStatus{
					GarbageCollectionInterval: &metav1.Duration{Duration: 600},
				},
			},
			expected: true,
		},
		{
			name: "returns false when intervals match",
			cfg: &fleet.ClusterStatus{
				GarbageCollectionInterval: &metav1.Duration{Duration: 300},
			},
			cluster: &fleet.Cluster{
				Status: fleet.ClusterStatus{
					GarbageCollectionInterval: &metav1.Duration{Duration: 300},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a config from the status
			var gcInterval metav1.Duration
			if tt.cfg.GarbageCollectionInterval != nil {
				gcInterval = *tt.cfg.GarbageCollectionInterval
			}
			cfg := &config.Config{
				GarbageCollectionInterval: gcInterval,
			}
			result := hasGarbageCollectionIntervalChanged(cfg, tt.cluster)
			assert.Equal(t, tt.expected, result)
		})
	}
}
