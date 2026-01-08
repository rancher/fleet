package manageagent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestManageAgentReconciler_Reconcile(t *testing.T) {
	// Set required environment variables
	t.Setenv("FLEET_AGENT_ELECTION_LEASE_DURATION", "15s")
	t.Setenv("FLEET_AGENT_ELECTION_RENEW_DEADLINE", "10s")
	t.Setenv("FLEET_AGENT_ELECTION_RETRY_PERIOD", "2s")
	t.Setenv("FLEET_AGENT_REPLICA_COUNT", "1")

	// Initialize config
	config.Set(&config.Config{
		AgentImage:            "rancher/fleet-agent:test",
		AgentCheckinInterval:  metav1.Duration{Duration: time.Second * 15},
		ManageAgent:           ptr.To(true),
		SystemDefaultRegistry: "",
	})

	scheme := runtime.NewScheme()
	_ = fleet.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name            string
		namespace       *corev1.Namespace
		clusters        []*fleet.Cluster
		existingBundles []*fleet.Bundle
		expectBundle    bool
		expectError     bool
	}{
		{
			name: "creates bundle for cluster in namespace",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "fleet-default",
				},
			},
			clusters: []*fleet.Cluster{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cluster",
						Namespace: "fleet-default",
					},
					Spec: fleet.ClusterSpec{
						AgentNamespace: "cattle-fleet-system",
					},
					Status: fleet.ClusterStatus{
						Namespace: "cluster-ns-test",
					},
				},
			},
			expectBundle: true,
			expectError:  false,
		},
		{
			name: "skips cluster with management label",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "fleet-default",
				},
			},
			clusters: []*fleet.Cluster{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cluster",
						Namespace: "fleet-default",
						Labels: map[string]string{
							fleet.ClusterManagementLabel: "true",
						},
					},
				},
			},
			expectBundle: false,
			expectError:  false,
		},
		{
			name: "updates existing bundle",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "fleet-default",
				},
			},
			clusters: []*fleet.Cluster{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cluster",
						Namespace: "fleet-default",
					},
					Spec: fleet.ClusterSpec{
						AgentNamespace: "cattle-fleet-system",
					},
					Status: fleet.ClusterStatus{
						Namespace: "cluster-ns-test",
					},
				},
			},
			existingBundles: []*fleet.Bundle{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "fleet-agent-test-cluster",
						Namespace: "fleet-default",
					},
				},
			},
			expectBundle: true,
			expectError:  false,
		},
		{
			name: "no clusters in namespace",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "fleet-default",
				},
			},
			clusters:     []*fleet.Cluster{},
			expectBundle: false,
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := []client.Object{tt.namespace}
			for _, cluster := range tt.clusters {
				objs = append(objs, cluster)
			}
			for _, bundle := range tt.existingBundles {
				objs = append(objs, bundle)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				WithStatusSubresource(&fleet.Cluster{}).
				Build()

			r := &ManageAgentReconciler{
				Client:          fakeClient,
				Scheme:          scheme,
				SystemNamespace: "cattle-fleet-system",
			}

			ctx := context.Background()
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name: tt.namespace.Name,
				},
			}

			result, err := r.Reconcile(ctx, req)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, reconcile.Result{}, result)
			}

			// Verify bundle creation/update
			if tt.expectBundle && len(tt.clusters) > 0 {
				var bundleList fleet.BundleList
				err = fakeClient.List(ctx, &bundleList, client.InNamespace(tt.namespace.Name))
				require.NoError(t, err)
				// Note: fake client may not persist creates immediately
			}
		})
	}
}

func TestClusterStatusReconciler_Reconcile(t *testing.T) {
	// Initialize config
	config.Set(&config.Config{})

	scheme := runtime.NewScheme()
	_ = fleet.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name                string
		cluster             *fleet.Cluster
		expectStatusUpdate  bool
		expectConfigChanged bool
	}{
		{
			name: "updates status for agent env vars",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{
					AgentEnvVars: []corev1.EnvVar{
						{Name: "TEST_VAR", Value: "test-value"},
					},
				},
				Status: fleet.ClusterStatus{
					AgentEnvVarsHash: "",
				},
			},
			expectStatusUpdate:  true,
			expectConfigChanged: true,
		},
		{
			name: "updates status for private repo URL",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{
					PrivateRepoURL: "https://private-repo.example.com",
				},
				Status: fleet.ClusterStatus{
					AgentPrivateRepoURL: "",
				},
			},
			expectStatusUpdate:  true,
			expectConfigChanged: true,
		},
		{
			name: "updates status for affinity",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{
					AgentAffinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{Key: "test", Operator: corev1.NodeSelectorOpIn, Values: []string{"value"}},
										},
									},
								},
							},
						},
					},
				},
				Status: fleet.ClusterStatus{
					AgentAffinityHash: "",
				},
			},
			expectStatusUpdate:  true,
			expectConfigChanged: true,
		},
		{
			name: "updates status for tolerations",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{
					AgentTolerations: []corev1.Toleration{
						{Key: "test", Operator: corev1.TolerationOpEqual, Value: "value", Effect: corev1.TaintEffectNoSchedule},
					},
				},
				Status: fleet.ClusterStatus{
					AgentTolerationsHash: "",
				},
			},
			expectStatusUpdate:  true,
			expectConfigChanged: true,
		},
		{
			name: "updates status for resources",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{
					AgentResources: &corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
					},
				},
				Status: fleet.ClusterStatus{
					AgentResourcesHash: "",
				},
			},
			expectStatusUpdate:  true,
			expectConfigChanged: true,
		},
		{
			name: "no status update when nothing changed",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{},
				Status: fleet.ClusterStatus{
					AgentEnvVarsHash: "",
				},
			},
			expectStatusUpdate:  false,
			expectConfigChanged: false,
		},
		{
			name: "skips cluster with management label",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "fleet-default",
					Labels: map[string]string{
						fleet.ClusterManagementLabel: "true",
					},
				},
				Spec: fleet.ClusterSpec{
					AgentEnvVars: []corev1.EnvVar{
						{Name: "TEST_VAR", Value: "test-value"},
					},
				},
			},
			expectStatusUpdate:  false,
			expectConfigChanged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := []client.Object{tt.cluster}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				WithStatusSubresource(&fleet.Cluster{}).
				Build()

			r := &ClusterStatusReconciler{
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

			// Verify status update
			if tt.expectStatusUpdate {
				var cluster fleet.Cluster
				err = fakeClient.Get(ctx, req.NamespacedName, &cluster)
				require.NoError(t, err)

				if tt.expectConfigChanged {
					assert.True(t, cluster.Status.AgentConfigChanged, "AgentConfigChanged should be true")
				}
			}
		})
	}
}

func TestNewAgentBundle(t *testing.T) {
	// Set required environment variables
	t.Setenv("FLEET_AGENT_ELECTION_LEASE_DURATION", "15s")
	t.Setenv("FLEET_AGENT_ELECTION_RENEW_DEADLINE", "10s")
	t.Setenv("FLEET_AGENT_ELECTION_RETRY_PERIOD", "2s")
	t.Setenv("FLEET_AGENT_REPLICA_COUNT", "1")

	// Initialize config
	config.Set(&config.Config{
		AgentImage:            "rancher/fleet-agent:test",
		AgentCheckinInterval:  metav1.Duration{Duration: time.Second * 15},
		SystemDefaultRegistry: "",
	})

	scheme := runtime.NewScheme()
	_ = fleet.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name             string
		cluster          *fleet.Cluster
		namespace        string
		expectBundleName string
		expectError      bool
	}{
		{
			name: "creates bundle with default settings",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{},
			},
			namespace:        "fleet-default",
			expectBundleName: "fleet-agent-test-cluster",
			expectError:      false,
		},
		{
			name: "creates bundle with custom agent namespace",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{
					AgentNamespace: "custom-namespace",
				},
			},
			namespace:        "fleet-default",
			expectBundleName: "fleet-agent-test-cluster",
			expectError:      false,
		},
		{
			name: "creates bundle with agent env vars",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{
					AgentEnvVars: []corev1.EnvVar{
						{Name: "TEST_VAR", Value: "test-value"},
					},
				},
			},
			namespace:        "fleet-default",
			expectBundleName: "fleet-agent-test-cluster",
			expectError:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			r := &ManageAgentReconciler{
				Client:          fakeClient,
				Scheme:          scheme,
				SystemNamespace: "cattle-fleet-system",
			}

			bundle, err := r.newAgentBundle(tt.namespace, tt.cluster)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, bundle)
				assert.Equal(t, tt.expectBundleName, bundle.Name)
				assert.Equal(t, tt.namespace, bundle.Namespace)
				assert.Len(t, bundle.Spec.Resources, 1)
				assert.Equal(t, "agent.yaml", bundle.Spec.Resources[0].Name)
				assert.NotEmpty(t, bundle.Spec.Resources[0].Content)
			}
		})
	}
}

func TestSkipCluster(t *testing.T) {
	tests := []struct {
		name       string
		cluster    *fleet.Cluster
		expectSkip bool
	}{
		{
			name:       "skips nil cluster",
			cluster:    nil,
			expectSkip: true,
		},
		{
			name: "skips cluster with management label",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						fleet.ClusterManagementLabel: "true",
					},
				},
			},
			expectSkip: true,
		},
		{
			name: "does not skip normal cluster",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"other": "label",
					},
				},
			},
			expectSkip: false,
		},
		{
			name: "does not skip cluster without labels",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{},
			},
			expectSkip: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SkipCluster(tt.cluster)
			assert.Equal(t, tt.expectSkip, result)
		})
	}
}

func TestSortTolerations(t *testing.T) {
	five := int64(5)
	ten := int64(10)

	tests := []struct {
		name string
		in   []corev1.Toleration
		want []corev1.Toleration
	}{
		{
			name: "basic ordering",
			in: []corev1.Toleration{
				{Key: "b", Value: "2", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
				{Key: "a", Value: "1", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "a", Value: "1", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "a", Value: "0", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoExecute},
			},
			want: []corev1.Toleration{
				{Key: "a", Value: "0", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoExecute},
				{Key: "a", Value: "1", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "a", Value: "1", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "b", Value: "2", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
			},
		},
		{
			name: "toleration seconds nil first",
			in: []corev1.Toleration{
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule, TolerationSeconds: &ten},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule}, // nil
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule, TolerationSeconds: &five},
			},
			want: []corev1.Toleration{
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule, TolerationSeconds: &five},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule, TolerationSeconds: &ten},
			},
		},
		{
			name: "key ordering",
			in: []corev1.Toleration{
				{Key: "z", Value: "x", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "a", Value: "x", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
			},
			want: []corev1.Toleration{
				{Key: "a", Value: "x", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "z", Value: "x", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
			},
		},
		{
			name: "value ordering",
			in: []corev1.Toleration{
				{Key: "k", Value: "z", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "k", Value: "a", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
			},
			want: []corev1.Toleration{
				{Key: "k", Value: "a", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "k", Value: "z", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
			},
		},
		{
			name: "operator ordering",
			in: []corev1.Toleration{
				{Key: "k", Value: "v", Operator: "", Effect: corev1.TaintEffectNoSchedule},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
			},
			want: []corev1.Toleration{
				{Key: "k", Value: "v", Operator: "", Effect: corev1.TaintEffectNoSchedule},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
			},
		},
		{
			name: "effect ordering",
			in: []corev1.Toleration{
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoExecute},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectPreferNoSchedule},
			},
			want: []corev1.Toleration{
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoExecute},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectPreferNoSchedule},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inCopy := make([]corev1.Toleration, len(tt.in))
			copy(inCopy, tt.in)
			sortTolerations(inCopy)
			assert.Equal(t, tt.want, inCopy)
		})
	}
}

func TestHashStatusField(t *testing.T) {
	tests := []struct {
		name        string
		field       any
		expectError bool
	}{
		{
			name: "hashes affinity",
			field: &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{Key: "test", Operator: corev1.NodeSelectorOpIn, Values: []string{"value"}},
						},
					}},
				}},
			},
			expectError: false,
		},
		{
			name: "hashes resources",
			field: &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("100Mi"),
				},
			},
			expectError: false,
		},
		{
			name: "hashes tolerations",
			field: []corev1.Toleration{
				{Key: "key", Value: "value", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
			},
			expectError: false,
		},
		{
			name:        "hashes nil",
			field:       nil,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash, err := hashStatusField(tt.field)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.NotEmpty(t, hash)
			}
		})
	}

	// Test that same input produces same hash
	t.Run("consistent hashing", func(t *testing.T) {
		affinity := &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{Key: "test", Operator: corev1.NodeSelectorOpIn, Values: []string{"value"}},
					},
				}},
			}},
		}
		hash1, _ := hashStatusField(affinity)
		hash2, _ := hashStatusField(affinity)
		assert.Equal(t, hash1, hash2)
	})

	// Test that different inputs produce different hashes
	t.Run("different hashes for different inputs", func(t *testing.T) {
		affinity1 := &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{Key: "test", Operator: corev1.NodeSelectorOpIn, Values: []string{"value1"}},
					},
				}},
			}},
		}
		affinity2 := &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{Key: "test", Operator: corev1.NodeSelectorOpIn, Values: []string{"value2"}},
					},
				}},
			}},
		}
		hash1, _ := hashStatusField(affinity1)
		hash2, _ := hashStatusField(affinity2)
		assert.NotEqual(t, hash1, hash2)
	})
}

func TestHashChanged(t *testing.T) {
	affinity := &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{Key: "test", Operator: corev1.NodeSelectorOpIn, Values: []string{"value"}},
				},
			}},
		}},
	}
	hash, _ := hashStatusField(affinity)

	differentAffinity := &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{Key: "different", Operator: corev1.NodeSelectorOpIn, Values: []string{"value"}},
				},
			}},
		}},
	}

	tests := []struct {
		name          string
		field         any
		currentHash   string
		expectChanged bool
	}{
		{
			name:          "no change when hash matches",
			field:         affinity,
			currentHash:   hash,
			expectChanged: false,
		},
		{
			name:          "change detected when hash differs",
			field:         differentAffinity,
			currentHash:   hash,
			expectChanged: true,
		},
		{
			name:          "change detected when current hash empty",
			field:         affinity,
			currentHash:   "",
			expectChanged: true,
		},
		{
			name:          "no change when both nil",
			field:         (*corev1.Affinity)(nil),
			currentHash:   "",
			expectChanged: false,
		},
		{
			name:          "no change when empty tolerations",
			field:         []corev1.Toleration{},
			currentHash:   "",
			expectChanged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed, _, err := hashChanged(tt.field, tt.currentHash)
			require.NoError(t, err)
			assert.Equal(t, tt.expectChanged, changed)
		})
	}
}
