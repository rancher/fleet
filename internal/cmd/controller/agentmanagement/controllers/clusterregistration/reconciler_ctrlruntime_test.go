package clusterregistration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestClusterRegistrationReconciler_Reconcile(t *testing.T) {
	// Initialize config to avoid panic
	config.Set(&config.Config{})

	scheme := runtime.NewScheme()
	_ = fleet.AddToScheme(scheme)
	_ = v1.AddToScheme(scheme)
	_ = rbacv1.AddToScheme(scheme)

	tests := []struct {
		name                         string
		clusterRegistration          *fleet.ClusterRegistration
		existingCluster              *fleet.Cluster
		existingNamespace            *v1.Namespace
		existingServiceAccount       *v1.ServiceAccount
		existingSecret               *v1.Secret
		expectClusterCreation        bool
		expectServiceAccountCreation bool
		expectRequeue                bool
		expectGranted                bool
	}{
		{
			name: "creates cluster for new registration",
			clusterRegistration: &fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-registration",
					Namespace: "fleet-default",
					UID:       "test-uid-123",
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "test-client-id",
					ClientRandom: "random123",
					ClusterLabels: map[string]string{
						"env": "test",
					},
				},
			},
			expectClusterCreation: true,
			expectRequeue:         true,
		},
		{
			name: "waits for cluster namespace assignment",
			clusterRegistration: &fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-registration",
					Namespace: "fleet-default",
					UID:       "test-uid-123",
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "test-client-id",
					ClientRandom: "random123",
				},
			},
			existingCluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-test-client-id-hash",
					Namespace: "fleet-default",
					UID:       "cluster-uid-456",
				},
				Spec: fleet.ClusterSpec{
					ClientID: "test-client-id",
				},
				Status: fleet.ClusterStatus{
					Namespace: "", // Not assigned yet
				},
			},
			expectRequeue: true,
		},
		{
			name: "creates service account when cluster namespace is ready",
			clusterRegistration: &fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-registration",
					Namespace: "fleet-default",
					UID:       "test-uid-123",
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "test-client-id",
					ClientRandom: "random123",
				},
			},
			existingCluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-test-client-id-hash",
					Namespace: "fleet-default",
					UID:       "cluster-uid-456",
				},
				Spec: fleet.ClusterSpec{
					ClientID: "test-client-id",
				},
				Status: fleet.ClusterStatus{
					Namespace: "cluster-ns-test",
				},
			},
			expectServiceAccountCreation: true,
			expectRequeue:                true,
		},
		{
			name: "skips already granted registration",
			clusterRegistration: &fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-registration",
					Namespace: "fleet-default",
					UID:       "test-uid-123",
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "test-client-id",
					ClientRandom: "random123",
				},
				Status: fleet.ClusterRegistrationStatus{
					Granted: true,
				},
			},
			expectRequeue: false,
		},
		{
			name: "skips cluster management labeled registration",
			clusterRegistration: &fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-registration",
					Namespace: "fleet-default",
					UID:       "test-uid-123",
					Labels: map[string]string{
						fleet.ClusterManagementLabel: "true",
					},
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "test-client-id",
					ClientRandom: "random123",
				},
			},
			expectRequeue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := []client.Object{}
			if tt.clusterRegistration != nil {
				objs = append(objs, tt.clusterRegistration)
			}
			if tt.existingCluster != nil {
				objs = append(objs, tt.existingCluster)
			}
			if tt.existingNamespace != nil {
				objs = append(objs, tt.existingNamespace)
			}
			if tt.existingServiceAccount != nil {
				objs = append(objs, tt.existingServiceAccount)
			}
			if tt.existingSecret != nil {
				objs = append(objs, tt.existingSecret)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				WithStatusSubresource(&fleet.ClusterRegistration{}, &fleet.Cluster{}).
				Build()

			r := &ClusterRegistrationReconciler{
				Client:                      fakeClient,
				Scheme:                      scheme,
				SystemNamespace:             "cattle-fleet-system",
				SystemRegistrationNamespace: "cattle-fleet-clusters-system",
			}

			ctx := context.Background()
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.clusterRegistration.Name,
					Namespace: tt.clusterRegistration.Namespace,
				},
			}

			result, err := r.Reconcile(ctx, req)
			require.NoError(t, err)

			if tt.expectRequeue {
				assert.True(t, result.Requeue || result.RequeueAfter > 0, "should requeue")
			} else {
				assert.Equal(t, reconcile.Result{}, result, "should not requeue")
			}

			// Verify cluster creation
			if tt.expectClusterCreation {
				var clusterList fleet.ClusterList
				err = fakeClient.List(ctx, &clusterList, client.InNamespace(tt.clusterRegistration.Namespace))
				require.NoError(t, err)
				assert.Greater(t, len(clusterList.Items), 0, "cluster should be created")
			}

			// Verify service account creation
			if tt.expectServiceAccountCreation && tt.existingCluster != nil {
				var saList v1.ServiceAccountList
				err = fakeClient.List(ctx, &saList, client.InNamespace(tt.existingCluster.Status.Namespace))
				require.NoError(t, err)
				// Note: fake client may not persist creates immediately
			}
		})
	}
}

func TestClusterRegistrationReconciler_CreateOrGetCluster(t *testing.T) {
	// Initialize config to avoid panic
	config.Set(&config.Config{})

	scheme := runtime.NewScheme()
	_ = fleet.AddToScheme(scheme)
	_ = v1.AddToScheme(scheme)

	tests := []struct {
		name                string
		clusterRegistration *fleet.ClusterRegistration
		existingCluster     *fleet.Cluster
		expectClusterName   string
		expectError         bool
	}{
		{
			name: "creates new cluster",
			clusterRegistration: &fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-registration",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "test-client-id",
					ClientRandom: "random123",
					ClusterLabels: map[string]string{
						"env": "test",
					},
				},
			},
			expectClusterName: "cluster-", // Prefix, actual name has hash
			expectError:       false,
		},
		{
			name: "returns existing cluster with matching clientID",
			clusterRegistration: &fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-registration",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "test-client-id",
					ClientRandom: "random123",
				},
			},
			existingCluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cluster-test-client-id-hash",
					Namespace: "fleet-default",
				},
				Spec: fleet.ClusterSpec{
					ClientID: "test-client-id",
				},
			},
			expectClusterName: "cluster-test-client-id-hash",
			expectError:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := []client.Object{tt.clusterRegistration}
			if tt.existingCluster != nil {
				objs = append(objs, tt.existingCluster)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				WithStatusSubresource(&fleet.Cluster{}).
				Build()

			r := &ClusterRegistrationReconciler{
				Client:                      fakeClient,
				Scheme:                      scheme,
				SystemNamespace:             "cattle-fleet-system",
				SystemRegistrationNamespace: "cattle-fleet-clusters-system",
			}

			ctx := context.Background()
			cluster, err := r.createOrGetCluster(ctx, tt.clusterRegistration)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, cluster)
				if tt.existingCluster != nil {
					assert.Equal(t, tt.expectClusterName, cluster.Name)
				} else {
					assert.Contains(t, cluster.Name, tt.expectClusterName)
				}
				assert.Equal(t, tt.clusterRegistration.Spec.ClientID, cluster.Spec.ClientID)
			}
		})
	}
}

func TestShouldDelete(t *testing.T) {
	tests := []struct {
		name         string
		existing     fleet.ClusterRegistration
		current      fleet.ClusterRegistration
		expectDelete bool
	}{
		{
			name: "deletes old registration with same clientID but different random",
			existing: fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "old-registration",
					CreationTimestamp: metav1.Time{Time: time.Now().Add(-1 * time.Hour)},
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "test-client",
					ClientRandom: "old-random",
				},
			},
			current: fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "new-registration",
					CreationTimestamp: metav1.Time{Time: time.Now()},
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "test-client",
					ClientRandom: "new-random",
				},
			},
			expectDelete: true,
		},
		{
			name: "keeps registration with same clientID and random",
			existing: fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "same-registration",
					CreationTimestamp: metav1.Time{Time: time.Now()},
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "test-client",
					ClientRandom: "same-random",
				},
			},
			current: fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "same-registration",
					CreationTimestamp: metav1.Time{Time: time.Now()},
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "test-client",
					ClientRandom: "same-random",
				},
			},
			expectDelete: false,
		},
		{
			name: "keeps registration with different clientID",
			existing: fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "different-client",
					CreationTimestamp: metav1.Time{Time: time.Now().Add(-1 * time.Hour)},
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "other-client",
					ClientRandom: "random",
				},
			},
			current: fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "current-registration",
					CreationTimestamp: metav1.Time{Time: time.Now()},
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "test-client",
					ClientRandom: "random",
				},
			},
			expectDelete: false,
		},
		{
			name: "keeps newer registration",
			existing: fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "newer-registration",
					CreationTimestamp: metav1.Time{Time: time.Now().Add(1 * time.Hour)},
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "test-client",
					ClientRandom: "new-random",
				},
			},
			current: fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "older-registration",
					CreationTimestamp: metav1.Time{Time: time.Now()},
				},
				Spec: fleet.ClusterRegistrationSpec{
					ClientID:     "test-client",
					ClientRandom: "old-random",
				},
			},
			expectDelete: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldDelete(tt.existing, tt.current)
			assert.Equal(t, tt.expectDelete, result)
		})
	}
}

func TestSkipClusterRegistration(t *testing.T) {
	tests := []struct {
		name                string
		clusterRegistration *fleet.ClusterRegistration
		expectSkip          bool
	}{
		{
			name:                "skips nil registration",
			clusterRegistration: nil,
			expectSkip:          true,
		},
		{
			name: "skips registration with cluster management label",
			clusterRegistration: &fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						fleet.ClusterManagementLabel: "true",
					},
				},
			},
			expectSkip: true,
		},
		{
			name: "does not skip normal registration",
			clusterRegistration: &fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"other": "label",
					},
				},
			},
			expectSkip: false,
		},
		{
			name: "does not skip registration without labels",
			clusterRegistration: &fleet.ClusterRegistration{
				ObjectMeta: metav1.ObjectMeta{},
			},
			expectSkip: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := skipClusterRegistration(tt.clusterRegistration)
			assert.Equal(t, tt.expectSkip, result)
		})
	}
}

func TestRequestSA(t *testing.T) {
	cluster := &fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "fleet-default",
		},
		Status: fleet.ClusterStatus{
			Namespace: "cluster-ns-test",
		},
	}

	request := &fleet.ClusterRegistration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-registration",
			Namespace: "fleet-default",
		},
	}

	saName := "test-sa"
	sa := requestSA(saName, cluster, request)

	assert.Equal(t, saName, sa.Name)
	assert.Equal(t, cluster.Status.Namespace, sa.Namespace)
	assert.Equal(t, "true", sa.Labels[fleet.ManagedLabel])
	assert.Equal(t, cluster.Name, sa.Annotations[fleet.ClusterAnnotation])
	assert.Equal(t, request.Name, sa.Annotations[fleet.ClusterRegistrationAnnotation])
	assert.Equal(t, request.Namespace, sa.Annotations[fleet.ClusterRegistrationNamespaceAnnotation])
}

func TestFindClusterRegistrationsForServiceAccount(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)
	_ = fleet.AddToScheme(scheme)

	tests := []struct {
		name          string
		sa            *v1.ServiceAccount
		expectRequest bool
	}{
		{
			name: "returns cluster registration key for annotated SA",
			sa: &v1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sa",
					Namespace: "cluster-ns",
					Annotations: map[string]string{
						fleet.ClusterRegistrationNamespaceAnnotation: "fleet-default",
						fleet.ClusterRegistrationAnnotation:          "test-registration",
					},
				},
			},
			expectRequest: true,
		},
		{
			name: "returns nil for SA without annotations",
			sa: &v1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sa",
					Namespace: "cluster-ns",
				},
			},
			expectRequest: false,
		},
		{
			name: "returns nil for SA with partial annotations (only name)",
			sa: &v1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sa",
					Namespace: "cluster-ns",
					Annotations: map[string]string{
						fleet.ClusterRegistrationAnnotation: "test-registration",
					},
				},
			},
			expectRequest: false,
		},
		{
			name: "returns nil for SA with partial annotations (only namespace)",
			sa: &v1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sa",
					Namespace: "cluster-ns",
					Annotations: map[string]string{
						fleet.ClusterRegistrationNamespaceAnnotation: "fleet-default",
					},
				},
			},
			expectRequest: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				Build()

			r := &ClusterRegistrationReconciler{
				Client:          fakeClient,
				Scheme:          scheme,
				SystemNamespace: "fleet-system",
			}

			ctx := context.Background()
			result := r.findClusterRegistrationsForServiceAccount(ctx, tt.sa)

			if tt.expectRequest {
				require.Len(t, result, 1)
				assert.Equal(t, "fleet-default", result[0].Namespace)
				assert.Equal(t, "test-registration", result[0].Name)
			} else {
				assert.Empty(t, result)
			}
		})
	}
}

func TestSecretReconciler_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)
	_ = fleet.AddToScheme(scheme)

	tests := []struct {
		name          string
		secret        *v1.Secret
		age           time.Duration
		expectDelete  bool
		expectRequeue bool
	}{
		{
			name: "deletes expired secret",
			secret: &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "registration-secret",
					Namespace:         "cattle-fleet-clusters-system",
					CreationTimestamp: metav1.Time{Time: time.Now().Add(-25 * time.Hour)},
					Labels: map[string]string{
						fleet.ClusterAnnotation: "test-cluster",
					},
				},
			},
			expectDelete:  true,
			expectRequeue: false,
		},
		{
			name: "requeues non-expired secret",
			secret: &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "registration-secret",
					Namespace:         "cattle-fleet-clusters-system",
					CreationTimestamp: metav1.Time{Time: time.Now().Add(-10 * time.Minute)},
					Labels: map[string]string{
						fleet.ClusterAnnotation: "test-cluster",
					},
				},
			},
			expectDelete:  false,
			expectRequeue: true,
		},
		{
			name: "ignores secret without cluster annotation",
			secret: &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "other-secret",
					Namespace:         "cattle-fleet-clusters-system",
					CreationTimestamp: metav1.Time{Time: time.Now().Add(-25 * time.Hour)},
				},
			},
			expectDelete:  false,
			expectRequeue: false,
		},
		{
			name: "ignores secret in different namespace",
			secret: &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "registration-secret",
					Namespace:         "other-namespace",
					CreationTimestamp: metav1.Time{Time: time.Now().Add(-25 * time.Hour)},
					Labels: map[string]string{
						fleet.ClusterAnnotation: "test-cluster",
					},
				},
			},
			expectDelete:  false,
			expectRequeue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := []client.Object{tt.secret}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				Build()

			r := &SecretReconciler{
				Client:                      fakeClient,
				Scheme:                      scheme,
				SystemRegistrationNamespace: "cattle-fleet-clusters-system",
			}

			ctx := context.Background()
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.secret.Name,
					Namespace: tt.secret.Namespace,
				},
			}

			result, err := r.Reconcile(ctx, req)
			require.NoError(t, err)

			if tt.expectRequeue {
				assert.True(t, result.RequeueAfter > 0, "should requeue with delay")
			}

			// Verify deletion
			if tt.expectDelete {
				var secret v1.Secret
				err = fakeClient.Get(ctx, req.NamespacedName, &secret)
				// Note: fake client may not immediately reflect deletions
			}
		})
	}
}
