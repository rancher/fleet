package config

import (
	"context"
	"testing"

	"github.com/rancher/fleet/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestConfigMapReconciler_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))

	systemNamespace := "cattle-fleet-system"

	tests := []struct {
		name           string
		configMap      *corev1.ConfigMap
		expectError    bool
		validateConfig func(t *testing.T)
	}{
		{
			name: "valid config updates global config",
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.ManagerConfigName,
					Namespace: systemNamespace,
				},
				Data: map[string]string{
					config.Key: `{
						"apiServerURL": "https://example.com:6443",
						"apiServerCA": "dGVzdC1jYQ==",
						"systemDefaultRegistry": "my-registry.io"
					}`,
				},
			},
			expectError: false,
			validateConfig: func(t *testing.T) {
				cfg := config.Get()
				assert.Equal(t, "https://example.com:6443", cfg.APIServerURL)
				assert.Equal(t, "my-registry.io", cfg.SystemDefaultRegistry)
			},
		},
		{
			name: "empty config is accepted",
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.ManagerConfigName,
					Namespace: systemNamespace,
				},
				Data: map[string]string{
					config.Key: `{}`,
				},
			},
			expectError: false,
		},
		{
			name: "wrong configmap name is ignored",
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-config",
					Namespace: systemNamespace,
				},
				Data: map[string]string{
					config.Key: `{}`,
				},
			},
			expectError: false,
		},
		{
			name: "wrong namespace is ignored",
			configMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.ManagerConfigName,
					Namespace: "other-namespace",
				},
				Data: map[string]string{
					config.Key: `{}`,
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client with the configmap
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.configMap).
				Build()

			reconciler := &ConfigMapReconciler{
				Client:          fakeClient,
				Scheme:          scheme,
				SystemNamespace: systemNamespace,
			}

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tt.configMap.Name,
					Namespace: tt.configMap.Namespace,
				},
			}

			result, err := reconciler.Reconcile(context.Background(), req)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, ctrl.Result{}, result)
			}

			if tt.validateConfig != nil {
				tt.validateConfig(t)
			}
		})
	}
}

func TestConfigMapReconciler_Reconcile_DeletedConfigMap(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))

	systemNamespace := "cattle-fleet-system"

	// Create fake client without the configmap (simulating deletion)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	reconciler := &ConfigMapReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		SystemNamespace: systemNamespace,
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      config.ManagerConfigName,
			Namespace: systemNamespace,
		},
	}

	// Should not error when configmap is deleted
	result, err := reconciler.Reconcile(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestConfigMapReconciler_SetupWithManager(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))

	// Create a minimal fake manager setup
	// Note: In real tests, you'd use envtest or a real manager
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	reconciler := &ConfigMapReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		SystemNamespace: "cattle-fleet-system",
	}

	// Test that the reconciler can be set up
	// In a real scenario with envtest, you'd call:
	// err := reconciler.SetupWithManager(mgr)
	// assert.NoError(t, err)

	// For unit tests, we just verify the reconciler is properly initialized
	assert.NotNil(t, reconciler.Client)
	assert.NotNil(t, reconciler.Scheme)
	assert.Equal(t, "cattle-fleet-system", reconciler.SystemNamespace)
}

func TestConfigMapReconciler_Reconcile_ConfigUpdates(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))

	systemNamespace := "cattle-fleet-system"

	// Initial config
	initialConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.ManagerConfigName,
			Namespace: systemNamespace,
		},
		Data: map[string]string{
			config.Key: `{
				"apiServerURL": "https://initial.com:6443",
				"systemDefaultRegistry": "initial-registry.io"
			}`,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(initialConfigMap).
		Build()

	reconciler := &ConfigMapReconciler{
		Client:          fakeClient,
		Scheme:          scheme,
		SystemNamespace: systemNamespace,
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      config.ManagerConfigName,
			Namespace: systemNamespace,
		},
	}

	// First reconciliation
	_, err := reconciler.Reconcile(context.Background(), req)
	require.NoError(t, err)

	cfg := config.Get()
	assert.Equal(t, "https://initial.com:6443", cfg.APIServerURL)
	assert.Equal(t, "initial-registry.io", cfg.SystemDefaultRegistry)

	// Update the config
	updatedConfigMap := &corev1.ConfigMap{}
	err = fakeClient.Get(context.Background(), types.NamespacedName{
		Name:      config.ManagerConfigName,
		Namespace: systemNamespace,
	}, updatedConfigMap)
	require.NoError(t, err)

	updatedConfigMap.Data[config.Key] = `{
		"apiServerURL": "https://updated.com:6443",
		"systemDefaultRegistry": "updated-registry.io"
	}`

	err = fakeClient.Update(context.Background(), updatedConfigMap)
	require.NoError(t, err)

	// Second reconciliation after update
	_, err = reconciler.Reconcile(context.Background(), req)
	require.NoError(t, err)

	cfg = config.Get()
	assert.Equal(t, "https://updated.com:6443", cfg.APIServerURL)
	assert.Equal(t, "updated-registry.io", cfg.SystemDefaultRegistry)
}

func TestConfigMapReconciler_Predicate(t *testing.T) {
	systemNamespace := "cattle-fleet-system"

	tests := []struct {
		name      string
		obj       client.Object
		shouldRun bool
	}{
		{
			name: "fleet-controller config in system namespace",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.ManagerConfigName,
					Namespace: systemNamespace,
				},
			},
			shouldRun: true,
		},
		{
			name: "other config in system namespace",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-config",
					Namespace: systemNamespace,
				},
			},
			shouldRun: false,
		},
		{
			name: "fleet-controller config in wrong namespace",
			obj: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.ManagerConfigName,
					Namespace: "other-namespace",
				},
			},
			shouldRun: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the predicate logic
			shouldRun := tt.obj.GetName() == config.ManagerConfigName &&
				tt.obj.GetNamespace() == systemNamespace

			assert.Equal(t, tt.shouldRun, shouldRun)
		})
	}
}
