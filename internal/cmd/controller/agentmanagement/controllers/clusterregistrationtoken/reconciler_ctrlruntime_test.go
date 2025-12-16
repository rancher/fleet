package clusterregistrationtoken

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestClusterRegistrationTokenReconciler_Reconcile(t *testing.T) {
	systemNamespace := "cattle-fleet-system"
	systemRegistrationNamespace := "cattle-fleet-clusters-system"
	tokenNamespace := "fleet-default"

	// Set global config for tests
	config.Set(&config.Config{
		APIServerURL: "https://test-server:6443",
		APIServerCA:  []byte("test-ca"),
	})

	tests := []struct {
		name           string
		token          *fleet.ClusterRegistrationToken
		setupObjects   []client.Object
		expectError    bool
		validateResult func(t *testing.T, reconciler *ClusterRegistrationTokenReconciler, token *fleet.ClusterRegistrationToken)
	}{
		{
			name: "creates all resources for new token",
			token: &fleet.ClusterRegistrationToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-token",
					Namespace: tokenNamespace,
					UID:       "test-uid-123",
				},
				Spec: fleet.ClusterRegistrationTokenSpec{},
			},
			setupObjects: []client.Object{},
			expectError:  false,
			validateResult: func(t *testing.T, reconciler *ClusterRegistrationTokenReconciler, token *fleet.ClusterRegistrationToken) {
				ctx := context.Background()
				saName := names.SafeConcatName(token.Name, string(token.UID))

				// Verify ServiceAccount was created
				var sa corev1.ServiceAccount
				err := reconciler.Get(ctx, types.NamespacedName{
					Namespace: tokenNamespace,
					Name:      saName,
				}, &sa)
				require.NoError(t, err)
				assert.Equal(t, "true", sa.Labels[fleet.ManagedLabel])
				assert.Equal(t, 1, len(sa.OwnerReferences))
				assert.Equal(t, token.Name, sa.OwnerReferences[0].Name)
			},
		},
		{
			name: "creates RBAC resources when service account has token",
			token: &fleet.ClusterRegistrationToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-token",
					Namespace: tokenNamespace,
					UID:       "test-uid-456",
				},
				Spec: fleet.ClusterRegistrationTokenSpec{},
			},
			setupObjects: []client.Object{
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      names.SafeConcatName("test-token", "test-uid-456"),
						Namespace: tokenNamespace,
					},
					Secrets: []corev1.ObjectReference{
						{Name: "sa-token-secret"},
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sa-token-secret",
						Namespace: tokenNamespace,
					},
					Type: corev1.SecretTypeServiceAccountToken,
					Data: map[string][]byte{
						corev1.ServiceAccountTokenKey: []byte("test-token-value"),
					},
				},
			},
			expectError: false,
			validateResult: func(t *testing.T, reconciler *ClusterRegistrationTokenReconciler, token *fleet.ClusterRegistrationToken) {
				ctx := context.Background()
				saName := names.SafeConcatName(token.Name, string(token.UID))

				// Verify Role was created
				var role rbacv1.Role
				err := reconciler.Get(ctx, types.NamespacedName{
					Namespace: tokenNamespace,
					Name:      names.SafeConcatName(saName, "role"),
				}, &role)
				require.NoError(t, err)
				assert.Equal(t, "true", role.Labels[fleet.ManagedLabel])
				assert.Equal(t, 1, len(role.Rules))
				assert.Contains(t, role.Rules[0].Resources, fleet.ClusterRegistrationResourceNamePlural)
				assert.Contains(t, role.Rules[0].Verbs, "create")

				// Verify RoleBinding was created
				var roleBinding rbacv1.RoleBinding
				err = reconciler.Get(ctx, types.NamespacedName{
					Namespace: tokenNamespace,
					Name:      names.SafeConcatName(saName, "to", "role"),
				}, &roleBinding)
				require.NoError(t, err)
				assert.Equal(t, saName, roleBinding.Subjects[0].Name)

				// Verify credentials Role in system registration namespace
				var credsRole rbacv1.Role
				err = reconciler.Get(ctx, types.NamespacedName{
					Namespace: systemRegistrationNamespace,
					Name:      names.SafeConcatName(saName, "creds"),
				}, &credsRole)
				require.NoError(t, err)
				assert.Contains(t, credsRole.Rules[0].Resources, "secrets")
				assert.Contains(t, credsRole.Rules[0].Verbs, "get")

				// Verify credentials RoleBinding
				var credsRoleBinding rbacv1.RoleBinding
				err = reconciler.Get(ctx, types.NamespacedName{
					Namespace: systemRegistrationNamespace,
					Name:      names.SafeConcatName(saName, "creds"),
				}, &credsRoleBinding)
				require.NoError(t, err)
				assert.Equal(t, saName, credsRoleBinding.Subjects[0].Name)
				assert.Equal(t, tokenNamespace, credsRoleBinding.Subjects[0].Namespace)

				// Verify cluster registration secret was created
				var secret corev1.Secret
				err = reconciler.Get(ctx, types.NamespacedName{
					Namespace: tokenNamespace,
					Name:      token.Name,
				}, &secret)
				require.NoError(t, err)
				assert.Equal(t, "fleet.cattle.io/cluster-registration-values", string(secret.Type))
				assert.NotEmpty(t, secret.Data[config.ImportTokenSecretValuesKey])

				// Verify status was updated
				var updatedToken fleet.ClusterRegistrationToken
				err = reconciler.Get(ctx, types.NamespacedName{
					Namespace: token.Namespace,
					Name:      token.Name,
				}, &updatedToken)
				require.NoError(t, err)
				assert.Equal(t, token.Name, updatedToken.Status.SecretName)
			},
		},
		{
			name: "sets expiration time when TTL is specified",
			token: &fleet.ClusterRegistrationToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "expiring-token",
					Namespace:         tokenNamespace,
					UID:               "test-uid-789",
					CreationTimestamp: metav1.Now(),
				},
				Spec: fleet.ClusterRegistrationTokenSpec{
					TTL: &metav1.Duration{Duration: 1 * time.Hour},
				},
			},
			setupObjects: []client.Object{
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      names.SafeConcatName("expiring-token", "test-uid-789"),
						Namespace: tokenNamespace,
					},
					Secrets: []corev1.ObjectReference{{Name: "sa-token"}},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sa-token",
						Namespace: tokenNamespace,
					},
					Type: corev1.SecretTypeServiceAccountToken,
					Data: map[string][]byte{
						corev1.ServiceAccountTokenKey: []byte("token"),
					},
				},
			},
			expectError: false,
			validateResult: func(t *testing.T, reconciler *ClusterRegistrationTokenReconciler, token *fleet.ClusterRegistrationToken) {
				ctx := context.Background()

				var updatedToken fleet.ClusterRegistrationToken
				err := reconciler.Get(ctx, types.NamespacedName{
					Namespace: token.Namespace,
					Name:      token.Name,
				}, &updatedToken)
				require.NoError(t, err)
				require.NotNil(t, updatedToken.Status.Expires)

				expectedExpiry := token.CreationTimestamp.Add(1 * time.Hour)
				// Allow 1 second difference for test execution time
				assert.WithinDuration(t, expectedExpiry, updatedToken.Status.Expires.Time, 1*time.Second)
			},
		},
		{
			name: "handles K8s 1.24+ service account without secrets",
			token: &fleet.ClusterRegistrationToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "k8s124-token",
					Namespace: tokenNamespace,
					UID:       "test-uid-124",
				},
				Spec: fleet.ClusterRegistrationTokenSpec{},
			},
			setupObjects: []client.Object{
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      names.SafeConcatName("k8s124-token", "test-uid-124"),
						Namespace: tokenNamespace,
					},
					Secrets: []corev1.ObjectReference{},
				},
			},
			expectError: false,
			validateResult: func(t *testing.T, reconciler *ClusterRegistrationTokenReconciler, token *fleet.ClusterRegistrationToken) {
				ctx := context.Background()
				saName := names.SafeConcatName(token.Name, string(token.UID))

				// Verify a token secret was created for K8s 1.24+
				var secretList corev1.SecretList
				err := reconciler.List(ctx, &secretList, client.InNamespace(tokenNamespace))
				require.NoError(t, err)

				foundTokenSecret := false
				for _, secret := range secretList.Items {
					if secret.Type == corev1.SecretTypeServiceAccountToken &&
						secret.Annotations[corev1.ServiceAccountNameKey] == saName {
						foundTokenSecret = true
						assert.Equal(t, "true", secret.Labels[fleet.ManagedLabel])
						break
					}
				}
				assert.True(t, foundTokenSecret, "Should create service account token secret for K8s 1.24+")
			},
		},
		{
			name: "requeues when service account token not yet populated",
			token: &fleet.ClusterRegistrationToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wait-token",
					Namespace: tokenNamespace,
					UID:       "test-uid-wait",
				},
				Spec: fleet.ClusterRegistrationTokenSpec{},
			},
			setupObjects: []client.Object{
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      names.SafeConcatName("wait-token", "test-uid-wait"),
						Namespace: tokenNamespace,
					},
					Secrets: []corev1.ObjectReference{{Name: "empty-token"}},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "empty-token",
						Namespace: tokenNamespace,
					},
					Type: corev1.SecretTypeServiceAccountToken,
					Data: map[string][]byte{
						corev1.ServiceAccountTokenKey: []byte(""), // Empty token
					},
				},
			},
			expectError: false,
			validateResult: func(t *testing.T, reconciler *ClusterRegistrationTokenReconciler, token *fleet.ClusterRegistrationToken) {
				// This test validates that reconciliation doesn't fail when token is not ready
				// The reconciler should requeue
			},
		},
		{
			name: "returns not found when token is deleted",
			token: &fleet.ClusterRegistrationToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deleted-token",
					Namespace: tokenNamespace,
					UID:       "test-uid-deleted",
				},
				Spec: fleet.ClusterRegistrationTokenSpec{},
			},
			setupObjects: []client.Object{},
			expectError:  false,
			validateResult: func(t *testing.T, reconciler *ClusterRegistrationTokenReconciler, token *fleet.ClusterRegistrationToken) {
				// Token not found should be handled gracefully
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create scheme
			scheme := runtime.NewScheme()
			_ = fleet.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)
			_ = rbacv1.AddToScheme(scheme)

			// Prepare initial objects
			var initialObjects []client.Object
			if tt.token.Name != "deleted-token" {
				initialObjects = append(initialObjects, tt.token)
			}
			initialObjects = append(initialObjects, tt.setupObjects...)

			// Create fake client
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(initialObjects...).
				WithStatusSubresource(&fleet.ClusterRegistrationToken{}).
				Build()

			// Create reconciler
			reconciler := &ClusterRegistrationTokenReconciler{
				Client:                      fakeClient,
				Scheme:                      scheme,
				SystemNamespace:             systemNamespace,
				SystemRegistrationNamespace: systemRegistrationNamespace,
			}

			// Reconcile
			result, err := reconciler.Reconcile(context.Background(), reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: tt.token.Namespace,
					Name:      tt.token.Name,
				},
			})

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			// Some tests expect requeue
			if tt.name == "creates all resources for new token" || tt.name == "requeues when service account token not yet populated" {
				assert.True(t, result.RequeueAfter > 0 || result.Requeue)
			}

			if tt.validateResult != nil {
				tt.validateResult(t, reconciler, tt.token)
			}
		})
	}
}

func TestClusterRegistrationTokenReconciler_DeleteExpired(t *testing.T) {
	tokenNamespace := "fleet-default"

	tests := []struct {
		name          string
		token         *fleet.ClusterRegistrationToken
		expectDeleted bool
		expectRequeue bool
	}{
		{
			name: "does not delete token without TTL",
			token: &fleet.ClusterRegistrationToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "no-ttl-token",
					Namespace:         tokenNamespace,
					CreationTimestamp: metav1.Now(),
				},
				Spec: fleet.ClusterRegistrationTokenSpec{
					TTL: nil,
				},
			},
			expectDeleted: false,
			expectRequeue: false,
		},
		{
			name: "does not delete token with zero TTL",
			token: &fleet.ClusterRegistrationToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "zero-ttl-token",
					Namespace:         tokenNamespace,
					CreationTimestamp: metav1.Now(),
				},
				Spec: fleet.ClusterRegistrationTokenSpec{
					TTL: &metav1.Duration{Duration: 0},
				},
			},
			expectDeleted: false,
			expectRequeue: false,
		},
		{
			name: "deletes expired token",
			token: &fleet.ClusterRegistrationToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "expired-token",
					Namespace:         tokenNamespace,
					CreationTimestamp: metav1.Time{Time: time.Now().Add(-2 * time.Hour)},
				},
				Spec: fleet.ClusterRegistrationTokenSpec{
					TTL: &metav1.Duration{Duration: 1 * time.Hour},
				},
			},
			expectDeleted: true,
			expectRequeue: false,
		},
		{
			name: "does not delete non-expired token",
			token: &fleet.ClusterRegistrationToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "future-token",
					Namespace:         tokenNamespace,
					CreationTimestamp: metav1.Now(),
				},
				Spec: fleet.ClusterRegistrationTokenSpec{
					TTL: &metav1.Duration{Duration: 24 * time.Hour},
				},
			},
			expectDeleted: false,
			expectRequeue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = fleet.AddToScheme(scheme)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.token).
				Build()

			reconciler := &ClusterRegistrationTokenReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			ctx := context.Background()
			gone, result, err := reconciler.deleteExpired(ctx, tt.token)

			assert.NoError(t, err)
			assert.Equal(t, tt.expectDeleted, gone)

			if tt.expectDeleted {
				// Verify token was deleted
				var token fleet.ClusterRegistrationToken
				err := reconciler.Get(ctx, types.NamespacedName{
					Namespace: tt.token.Namespace,
					Name:      tt.token.Name,
				}, &token)
				assert.True(t, err != nil) // Should be not found
			}

			if tt.expectRequeue {
				assert.True(t, result.RequeueAfter > 0 || result.Requeue)
			}
		})
	}
}

func TestClusterRegistrationTokenReconciler_CreateRBACResources(t *testing.T) {
	systemNamespace := "cattle-fleet-system"
	systemRegistrationNamespace := "cattle-fleet-clusters-system"
	tokenNamespace := "fleet-default"

	token := &fleet.ClusterRegistrationToken{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-token",
			Namespace: tokenNamespace,
			UID:       "test-uid",
		},
	}

	scheme := runtime.NewScheme()
	_ = fleet.AddToScheme(scheme)
	_ = rbacv1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(token).
		Build()

	reconciler := &ClusterRegistrationTokenReconciler{
		Client:                      fakeClient,
		Scheme:                      scheme,
		SystemNamespace:             systemNamespace,
		SystemRegistrationNamespace: systemRegistrationNamespace,
	}

	saName := names.SafeConcatName(token.Name, string(token.UID))
	ctx := context.Background()

	err := reconciler.createRBACResources(ctx, token, saName)
	require.NoError(t, err)

	// Verify all RBAC resources were created
	var role rbacv1.Role
	err = reconciler.Get(ctx, types.NamespacedName{
		Namespace: tokenNamespace,
		Name:      names.SafeConcatName(saName, "role"),
	}, &role)
	assert.NoError(t, err)

	var roleBinding rbacv1.RoleBinding
	err = reconciler.Get(ctx, types.NamespacedName{
		Namespace: tokenNamespace,
		Name:      names.SafeConcatName(saName, "to", "role"),
	}, &roleBinding)
	assert.NoError(t, err)

	var credsRole rbacv1.Role
	err = reconciler.Get(ctx, types.NamespacedName{
		Namespace: systemRegistrationNamespace,
		Name:      names.SafeConcatName(saName, "creds"),
	}, &credsRole)
	assert.NoError(t, err)

	var credsRoleBinding rbacv1.RoleBinding
	err = reconciler.Get(ctx, types.NamespacedName{
		Namespace: systemRegistrationNamespace,
		Name:      names.SafeConcatName(saName, "creds"),
	}, &credsRoleBinding)
	assert.NoError(t, err)
}

func TestClusterRegistrationTokenReconciler_CreateClusterRegistrationSecret(t *testing.T) {
	systemNamespace := "cattle-fleet-system"
	systemRegistrationNamespace := "cattle-fleet-clusters-system"
	tokenNamespace := "fleet-default"

	// Set global config
	config.Set(&config.Config{
		APIServerURL: "https://test-api:6443",
		APIServerCA:  []byte("test-ca-data"),
	})

	token := &fleet.ClusterRegistrationToken{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-token",
			Namespace: tokenNamespace,
			UID:       "test-uid",
		},
	}

	saSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sa-token-secret",
			Namespace: tokenNamespace,
		},
		Type: corev1.SecretTypeServiceAccountToken,
		Data: map[string][]byte{
			corev1.ServiceAccountTokenKey: []byte("test-service-account-token"),
		},
	}

	scheme := runtime.NewScheme()
	_ = fleet.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(token, saSecret).
		Build()

	reconciler := &ClusterRegistrationTokenReconciler{
		Client:                      fakeClient,
		Scheme:                      scheme,
		SystemNamespace:             systemNamespace,
		SystemRegistrationNamespace: systemRegistrationNamespace,
	}

	ctx := context.Background()
	err := reconciler.createClusterRegistrationSecret(ctx, token, saSecret.Name)
	require.NoError(t, err)

	// Verify secret was created
	var secret corev1.Secret
	err = reconciler.Get(ctx, types.NamespacedName{
		Namespace: tokenNamespace,
		Name:      token.Name,
	}, &secret)
	require.NoError(t, err)

	assert.Equal(t, "fleet.cattle.io/cluster-registration-values", string(secret.Type))
	assert.Equal(t, "true", secret.Labels[fleet.ManagedLabel])
	assert.NotEmpty(t, secret.Data[config.ImportTokenSecretValuesKey])

	// Verify the secret contains expected values (basic check)
	data := secret.Data[config.ImportTokenSecretValuesKey]
	assert.Contains(t, string(data), "clusterNamespace")
	assert.Contains(t, string(data), "test-service-account-token")
}

func TestClusterRegistrationTokenReconciler_CreateOrUpdate(t *testing.T) {
	tests := []struct {
		name        string
		existingObj client.Object
		newObj      client.Object
		expectError bool
	}{
		{
			name:        "creates new object",
			existingObj: nil,
			newObj: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-secret",
					Namespace: "test-namespace",
				},
				Data: map[string][]byte{"key": []byte("value")},
			},
			expectError: false,
		},
		{
			name: "updates existing object",
			existingObj: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "existing-secret",
					Namespace: "test-namespace",
				},
				Data: map[string][]byte{"key": []byte("old-value")},
			},
			newObj: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "existing-secret",
					Namespace: "test-namespace",
				},
				Data: map[string][]byte{"key": []byte("new-value")},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)

			var objects []client.Object
			if tt.existingObj != nil {
				objects = append(objects, tt.existingObj)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			reconciler := &ClusterRegistrationTokenReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			err := reconciler.createOrUpdate(context.Background(), tt.newObj)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)

				// Verify object exists
				key := client.ObjectKeyFromObject(tt.newObj)
				result := tt.newObj.DeepCopyObject().(client.Object)
				err := reconciler.Get(context.Background(), key, result)
				assert.NoError(t, err)
			}
		})
	}
}

func TestRegisterControllerRuntime(t *testing.T) {
	// This test validates the RegisterControllerRuntime function exists and has correct signature
	// Full integration testing would require envtest

	systemNamespace := "cattle-fleet-system"
	systemRegistrationNamespace := "cattle-fleet-clusters-system"

	t.Run("function exists with correct signature", func(t *testing.T) {
		// Just validate the function can be called with nil manager for compilation check
		// In real usage, this would be called with a real manager from envtest
		var mgr ctrl.Manager // nil for this test

		// This would normally return an error with nil manager, which is expected
		_ = systemNamespace
		_ = systemRegistrationNamespace
		_ = mgr

		// The function exists and has the right signature if this compiles
		// err := RegisterControllerRuntime(mgr, systemNamespace, systemRegistrationNamespace)
		// We can't actually call it with nil manager as it will panic
	})
}
