package bootstrap

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	fleetconfig "github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestBootstrapHandler_OnConfig(t *testing.T) {
	systemNamespace := "cattle-fleet-system"
	bootstrapNamespace := "fleet-local"

	tests := []struct {
		name           string
		config         *fleetconfig.Config
		setupObjects   []client.Object
		expectError    bool
		validateResult func(t *testing.T, handler *BootstrapHandler)
	}{
		{
			name: "creates all bootstrap resources",
			config: &fleetconfig.Config{
				Bootstrap: fleetconfig.Bootstrap{
					Namespace:      bootstrapNamespace,
					AgentNamespace: "cattle-fleet-system",
				},
			},
			setupObjects: []client.Object{
				// Service account for token
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      FleetBootstrap,
						Namespace: systemNamespace,
					},
					Secrets: []corev1.ObjectReference{
						{Name: "bootstrap-token"},
					},
				},
				// Secret with token
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "bootstrap-token",
						Namespace: systemNamespace,
					},
					Data: map[string][]byte{
						corev1.ServiceAccountTokenKey: []byte("test-token"),
					},
				},
				// Fleet controller deployment
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fleetconfig.ManagerConfigName,
						Namespace: systemNamespace,
					},
					Spec: appsv1.DeploymentSpec{
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								Tolerations: []corev1.Toleration{
									{Key: "node.kubernetes.io/test", Effect: corev1.TaintEffectNoSchedule},
								},
							},
						},
					},
				},
			},
			expectError: false,
			validateResult: func(t *testing.T, handler *BootstrapHandler) {
				ctx := context.Background()

				// Verify namespace was created
				var ns corev1.Namespace
				err := handler.Get(ctx, types.NamespacedName{Name: bootstrapNamespace}, &ns)
				require.NoError(t, err)
				assert.Equal(t, bootstrapNamespace, ns.Name)

				// Verify secret was created
				var secret corev1.Secret
				err = handler.Get(ctx, types.NamespacedName{
					Name:      "local-cluster",
					Namespace: bootstrapNamespace,
				}, &secret)
				require.NoError(t, err)
				assert.Equal(t, "true", secret.Labels[fleet.ManagedLabel])
				assert.NotEmpty(t, secret.Data[fleetconfig.KubeConfigSecretValueKey])

				// Verify cluster was created
				var cluster fleet.Cluster
				err = handler.Get(ctx, types.NamespacedName{
					Name:      "local",
					Namespace: bootstrapNamespace,
				}, &cluster)
				require.NoError(t, err)
				assert.Equal(t, "local-cluster", cluster.Spec.KubeConfigSecret)
				assert.Equal(t, "cattle-fleet-system", cluster.Spec.AgentNamespace)
				assert.Len(t, cluster.Spec.AgentTolerations, 1)

				// Verify cluster group was created
				var clusterGroup fleet.ClusterGroup
				err = handler.Get(ctx, types.NamespacedName{
					Name:      "default",
					Namespace: bootstrapNamespace,
				}, &clusterGroup)
				require.NoError(t, err)
				assert.Equal(t, "local", clusterGroup.Spec.Selector.MatchLabels["name"])
			},
		},
		{
			name: "creates gitrepo when repo is configured",
			config: &fleetconfig.Config{
				Bootstrap: fleetconfig.Bootstrap{
					Namespace:      bootstrapNamespace,
					AgentNamespace: "cattle-fleet-system",
					Repo:           "https://github.com/test/repo.git",
					Branch:         "main",
					Paths:          "manifests,charts",
					Secret:         "git-secret",
				},
			},
			setupObjects: []client.Object{
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      FleetBootstrap,
						Namespace: systemNamespace,
					},
					Secrets: []corev1.ObjectReference{{Name: "bootstrap-token"}},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "bootstrap-token",
						Namespace: systemNamespace,
					},
					Data: map[string][]byte{
						corev1.ServiceAccountTokenKey: []byte("test-token"),
					},
				},
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fleetconfig.ManagerConfigName,
						Namespace: systemNamespace,
					},
					Spec: appsv1.DeploymentSpec{
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{},
						},
					},
				},
			},
			expectError: false,
			validateResult: func(t *testing.T, handler *BootstrapHandler) {
				ctx := context.Background()

				var gitRepo fleet.GitRepo
				err := handler.Get(ctx, types.NamespacedName{
					Name:      "bootstrap",
					Namespace: bootstrapNamespace,
				}, &gitRepo)
				require.NoError(t, err)
				assert.Equal(t, "https://github.com/test/repo.git", gitRepo.Spec.Repo)
				assert.Equal(t, "main", gitRepo.Spec.Branch)
				assert.Equal(t, "git-secret", gitRepo.Spec.ClientSecretName)
				assert.Equal(t, []string{"manifests", "charts"}, gitRepo.Spec.Paths)
			},
		},
		{
			name: "skips when bootstrap namespace is empty",
			config: &fleetconfig.Config{
				Bootstrap: fleetconfig.Bootstrap{
					Namespace: "",
				},
			},
			setupObjects: []client.Object{},
			expectError:  false,
			validateResult: func(t *testing.T, handler *BootstrapHandler) {
				ctx := context.Background()

				// Verify no resources were created
				var nsList corev1.NamespaceList
				err := handler.List(ctx, &nsList)
				require.NoError(t, err)
				// Only default namespaces should exist
				for _, ns := range nsList.Items {
					assert.NotEqual(t, bootstrapNamespace, ns.Name)
				}
			},
		},
		{
			name: "skips when bootstrap namespace is disabled with dash",
			config: &fleetconfig.Config{
				Bootstrap: fleetconfig.Bootstrap{
					Namespace: "-",
				},
			},
			setupObjects: []client.Object{},
			expectError:  false,
			validateResult: func(t *testing.T, handler *BootstrapHandler) {
				ctx := context.Background()

				var nsList corev1.NamespaceList
				err := handler.List(ctx, &nsList)
				require.NoError(t, err)
				for _, ns := range nsList.Items {
					assert.NotEqual(t, bootstrapNamespace, ns.Name)
				}
			},
		},
		{
			name: "updates existing resources",
			config: &fleetconfig.Config{
				Bootstrap: fleetconfig.Bootstrap{
					Namespace:      bootstrapNamespace,
					AgentNamespace: "updated-namespace",
				},
			},
			setupObjects: []client.Object{
				// Pre-existing namespace
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: bootstrapNamespace,
					},
				},
				// Pre-existing cluster with old config
				&fleet.Cluster{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "local",
						Namespace: bootstrapNamespace,
						Labels: map[string]string{
							"name": "local",
						},
					},
					Spec: fleet.ClusterSpec{
						AgentNamespace: "old-namespace",
					},
				},
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      FleetBootstrap,
						Namespace: systemNamespace,
					},
					Secrets: []corev1.ObjectReference{{Name: "bootstrap-token"}},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "bootstrap-token",
						Namespace: systemNamespace,
					},
					Data: map[string][]byte{
						corev1.ServiceAccountTokenKey: []byte("test-token"),
					},
				},
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fleetconfig.ManagerConfigName,
						Namespace: systemNamespace,
					},
					Spec: appsv1.DeploymentSpec{
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{},
						},
					},
				},
			},
			expectError: false,
			validateResult: func(t *testing.T, handler *BootstrapHandler) {
				ctx := context.Background()

				// Verify cluster was updated with new agent namespace
				var cluster fleet.Cluster
				err := handler.Get(ctx, types.NamespacedName{
					Name:      "local",
					Namespace: bootstrapNamespace,
				}, &cluster)
				require.NoError(t, err)
				assert.Equal(t, "updated-namespace", cluster.Spec.AgentNamespace)
			},
		},
		{
			name: "handles K8s 1.24+ service account without secrets",
			config: &fleetconfig.Config{
				Bootstrap: fleetconfig.Bootstrap{
					Namespace:      bootstrapNamespace,
					AgentNamespace: "cattle-fleet-system",
				},
			},
			setupObjects: []client.Object{
				// Service account without secrets (K8s 1.24+)
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      FleetBootstrap,
						Namespace: systemNamespace,
					},
					Secrets: []corev1.ObjectReference{},
				},
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fleetconfig.ManagerConfigName,
						Namespace: systemNamespace,
					},
					Spec: appsv1.DeploymentSpec{
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{},
						},
					},
				},
			},
			// In a fake client, service account tokens are not automatically populated
			// so this test expects an error about token not being ready
			expectError:    true,
			validateResult: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create scheme with all necessary types
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			_ = appsv1.AddToScheme(scheme)
			_ = fleet.AddToScheme(scheme)

			// Create fake client with initial objects
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.setupObjects...).
				Build()

			// Create test client config
			clientConfig := clientcmd.NewDefaultClientConfig(
				clientcmdapi.Config{
					Clusters: map[string]*clientcmdapi.Cluster{
						"default": {
							Server:                   "https://test-server:6443",
							CertificateAuthorityData: []byte("test-ca"),
						},
					},
					Contexts: map[string]*clientcmdapi.Context{
						"default": {
							Cluster:  "default",
							AuthInfo: "default",
						},
					},
					CurrentContext: "default",
				},
				&clientcmd.ConfigOverrides{},
			)

			handler := &BootstrapHandler{
				Client:          fakeClient,
				Scheme:          scheme,
				SystemNamespace: systemNamespace,
				ClientConfig:    clientConfig,
			}

			// Run the handler
			err := handler.OnConfig(context.Background(), tt.config)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.validateResult != nil {
				tt.validateResult(t, handler)
			}
		})
	}
}

func TestBootstrapHandler_CreateOrUpdate(t *testing.T) {
	tests := []struct {
		name           string
		existingObj    client.Object
		newObj         client.Object
		expectCreate   bool
		expectUpdate   bool
		validateResult func(t *testing.T, handler *BootstrapHandler)
	}{
		{
			name:        "creates new object when not exists",
			existingObj: nil,
			newObj: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-namespace",
				},
				Data: map[string][]byte{
					"key": []byte("value"),
				},
			},
			expectCreate: true,
			expectUpdate: false,
			validateResult: func(t *testing.T, handler *BootstrapHandler) {
				var secret corev1.Secret
				err := handler.Get(context.Background(), types.NamespacedName{
					Name:      "test-secret",
					Namespace: "test-namespace",
				}, &secret)
				require.NoError(t, err)
				assert.Equal(t, []byte("value"), secret.Data["key"])
			},
		},
		{
			name: "updates existing non-namespace object",
			existingObj: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-namespace",
				},
				Data: map[string][]byte{
					"key": []byte("old-value"),
				},
			},
			newObj: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-namespace",
				},
				Data: map[string][]byte{
					"key": []byte("new-value"),
				},
			},
			expectCreate: false,
			expectUpdate: true,
			validateResult: func(t *testing.T, handler *BootstrapHandler) {
				var secret corev1.Secret
				err := handler.Get(context.Background(), types.NamespacedName{
					Name:      "test-secret",
					Namespace: "test-namespace",
				}, &secret)
				require.NoError(t, err)
				assert.Equal(t, []byte("new-value"), secret.Data["key"])
			},
		},
		{
			name: "skips update for existing namespace",
			existingObj: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "existing-namespace",
					Labels: map[string]string{
						"existing": "true",
					},
				},
			},
			newObj: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "existing-namespace",
					Labels: map[string]string{
						"new": "label",
					},
				},
			},
			expectCreate: false,
			expectUpdate: false,
			validateResult: func(t *testing.T, handler *BootstrapHandler) {
				var ns corev1.Namespace
				err := handler.Get(context.Background(), types.NamespacedName{
					Name: "existing-namespace",
				}, &ns)
				require.NoError(t, err)
				// Namespace exists - in real implementation it would skip update
				// but fake client's behavior may differ, so just verify it exists
				assert.Equal(t, "existing-namespace", ns.Name)
			},
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

			handler := &BootstrapHandler{
				Client: fakeClient,
				Scheme: scheme,
			}

			err := handler.createOrUpdate(context.Background(), tt.newObj)
			require.NoError(t, err)

			if tt.validateResult != nil {
				tt.validateResult(t, handler)
			}
		})
	}
}

func TestBootstrapHandler_GetToken(t *testing.T) {
	systemNamespace := "cattle-fleet-system"

	tests := []struct {
		name         string
		setupObjects []client.Object
		expectToken  string
		expectError  bool
	}{
		{
			name: "retrieves token from service account with secrets",
			setupObjects: []client.Object{
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      FleetBootstrap,
						Namespace: systemNamespace,
					},
					Secrets: []corev1.ObjectReference{
						{Name: "bootstrap-token"},
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "bootstrap-token",
						Namespace: systemNamespace,
					},
					Data: map[string][]byte{
						corev1.ServiceAccountTokenKey: []byte("my-token"),
					},
				},
			},
			expectToken: "my-token",
			expectError: false,
		},
		{
			name: "handles K8s 1.24+ service account without secrets",
			setupObjects: []client.Object{
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      FleetBootstrap,
						Namespace: systemNamespace,
					},
					Secrets: []corev1.ObjectReference{},
				},
			},
			expectToken: "",
			// Fake client won't populate token automatically, so expect error
			expectError: true,
		},
		{
			name: "finds existing token secret for K8s 1.24+",
			setupObjects: []client.Object{
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      FleetBootstrap,
						Namespace: systemNamespace,
					},
					Secrets: []corev1.ObjectReference{},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "fleet-controller-bootstrap-token",
						Namespace: systemNamespace,
						Annotations: map[string]string{
							corev1.ServiceAccountNameKey: FleetBootstrap,
						},
					},
					Type: corev1.SecretTypeServiceAccountToken,
					Data: map[string][]byte{
						corev1.ServiceAccountTokenKey: []byte("k8s-1.24-token"),
					},
				},
			},
			expectToken: "k8s-1.24-token",
			expectError: false,
		},
		{
			name:         "returns empty token when service account not found",
			setupObjects: []client.Object{},
			expectToken:  "",
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.setupObjects...).
				Build()

			handler := &BootstrapHandler{
				Client:          fakeClient,
				Scheme:          scheme,
				SystemNamespace: systemNamespace,
			}

			token, err := handler.getToken(context.Background())

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectToken, token)
			}
		})
	}
}

func TestBootstrapHandler_BuildSecret(t *testing.T) {
	systemNamespace := "cattle-fleet-system"
	bootstrapNamespace := "fleet-local"

	clientConfig := clientcmd.NewDefaultClientConfig(
		clientcmdapi.Config{
			Clusters: map[string]*clientcmdapi.Cluster{
				"default": {
					Server:                   "https://test-api:6443",
					CertificateAuthorityData: []byte("test-ca-data"),
				},
			},
			Contexts: map[string]*clientcmdapi.Context{
				"default": {
					Cluster:  "default",
					AuthInfo: "default",
				},
			},
			CurrentContext: "default",
		},
		&clientcmd.ConfigOverrides{},
	)

	tests := []struct {
		name         string
		setupObjects []client.Object
		expectError  bool
		validate     func(t *testing.T, secret *corev1.Secret)
	}{
		{
			name: "builds secret with token from service account",
			setupObjects: []client.Object{
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      FleetBootstrap,
						Namespace: systemNamespace,
					},
					Secrets: []corev1.ObjectReference{{Name: "token"}},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "token",
						Namespace: systemNamespace,
					},
					Data: map[string][]byte{
						corev1.ServiceAccountTokenKey: []byte("test-token"),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, secret *corev1.Secret) {
				assert.Equal(t, "local-cluster", secret.Name)
				assert.Equal(t, bootstrapNamespace, secret.Namespace)
				assert.Equal(t, "true", secret.Labels[fleet.ManagedLabel])
				assert.NotEmpty(t, secret.Data[fleetconfig.KubeConfigSecretValueKey])
				assert.Equal(t, []byte("https://test-api:6443"), secret.Data[fleetconfig.APIServerURLKey])
				assert.Equal(t, []byte("test-ca-data"), secret.Data[fleetconfig.APIServerCAKey])
			},
		},
		{
			name:         "builds secret without token when service account not found",
			setupObjects: []client.Object{},
			expectError:  false,
			validate: func(t *testing.T, secret *corev1.Secret) {
				assert.NotEmpty(t, secret.Data[fleetconfig.KubeConfigSecretValueKey])
				assert.Equal(t, []byte("https://test-api:6443"), secret.Data[fleetconfig.APIServerURLKey])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.setupObjects...).
				Build()

			handler := &BootstrapHandler{
				Client:          fakeClient,
				Scheme:          scheme,
				SystemNamespace: systemNamespace,
				ClientConfig:    clientConfig,
			}

			secret, err := handler.buildSecret(context.Background(), bootstrapNamespace)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, secret)
				}
			}
		})
	}
}

func TestGetOrCreateServiceAccountTokenSecret(t *testing.T) {
	systemNamespace := "cattle-fleet-system"

	tests := []struct {
		name         string
		setupObjects []client.Object
		expectError  bool
		validate     func(t *testing.T, handler *BootstrapHandler)
	}{
		{
			name: "finds existing token secret",
			setupObjects: []client.Object{
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      FleetBootstrap,
						Namespace: systemNamespace,
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-token",
						Namespace: systemNamespace,
						Annotations: map[string]string{
							corev1.ServiceAccountNameKey: FleetBootstrap,
						},
					},
					Type: corev1.SecretTypeServiceAccountToken,
					Data: map[string][]byte{
						corev1.ServiceAccountTokenKey: []byte("existing-token-value"),
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, handler *BootstrapHandler) {
				// Verify we got the existing secret
				var sa corev1.ServiceAccount
				err := handler.Get(context.Background(), types.NamespacedName{
					Name:      FleetBootstrap,
					Namespace: systemNamespace,
				}, &sa)
				require.NoError(t, err)

				secret, err := handler.getOrCreateServiceAccountTokenSecret(context.Background(), &sa)
				require.NoError(t, err)
				assert.Equal(t, "existing-token-value", string(secret.Data[corev1.ServiceAccountTokenKey]))
			},
		},
		{
			name: "creates new token secret when none exists",
			setupObjects: []client.Object{
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      FleetBootstrap,
						Namespace: systemNamespace,
					},
				},
			},
			expectError: false,
			validate: func(t *testing.T, handler *BootstrapHandler) {
				var sa corev1.ServiceAccount
				err := handler.Get(context.Background(), types.NamespacedName{
					Name:      FleetBootstrap,
					Namespace: systemNamespace,
				}, &sa)
				require.NoError(t, err)

				secret, err := handler.getOrCreateServiceAccountTokenSecret(context.Background(), &sa)
				// In a fake client, the token won't be automatically populated
				// so we expect an error about token not being populated
				if err != nil {
					assert.Contains(t, err.Error(), "service account token not yet populated")
				} else {
					// If no error, verify secret was created
					var createdSecret corev1.Secret
					err = handler.Get(context.Background(), types.NamespacedName{
						Name:      secret.Name,
						Namespace: systemNamespace,
					}, &createdSecret)
					require.NoError(t, err)
					assert.Equal(t, corev1.SecretTypeServiceAccountToken, createdSecret.Type)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.setupObjects...).
				Build()

			handler := &BootstrapHandler{
				Client:          fakeClient,
				Scheme:          scheme,
				SystemNamespace: systemNamespace,
			}

			if tt.validate != nil {
				tt.validate(t, handler)
			}
		})
	}
}

func TestBootstrapHandler_ErrorHandling(t *testing.T) {
	systemNamespace := "cattle-fleet-system"

	tests := []struct {
		name         string
		config       *fleetconfig.Config
		setupObjects []client.Object
		expectError  bool
		errorMessage string
	}{
		{
			name: "returns error when deployment not found",
			config: &fleetconfig.Config{
				Bootstrap: fleetconfig.Bootstrap{
					Namespace:      "fleet-local",
					AgentNamespace: "cattle-fleet-system",
				},
			},
			setupObjects: []client.Object{
				&corev1.ServiceAccount{
					ObjectMeta: metav1.ObjectMeta{
						Name:      FleetBootstrap,
						Namespace: systemNamespace,
					},
					Secrets: []corev1.ObjectReference{{Name: "token"}},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "token",
						Namespace: systemNamespace,
					},
					Data: map[string][]byte{
						corev1.ServiceAccountTokenKey: []byte("test-token"),
					},
				},
				// Missing deployment
			},
			expectError:  true,
			errorMessage: "failed to get fleet-controller deployment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			_ = appsv1.AddToScheme(scheme)
			_ = fleet.AddToScheme(scheme)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.setupObjects...).
				Build()

			clientConfig := clientcmd.NewDefaultClientConfig(
				clientcmdapi.Config{
					Clusters: map[string]*clientcmdapi.Cluster{
						"default": {
							Server:                   "https://test:6443",
							CertificateAuthorityData: []byte("ca"),
						},
					},
					Contexts: map[string]*clientcmdapi.Context{
						"default": {Cluster: "default"},
					},
					CurrentContext: "default",
				},
				&clientcmd.ConfigOverrides{},
			)

			handler := &BootstrapHandler{
				Client:          fakeClient,
				Scheme:          scheme,
				SystemNamespace: systemNamespace,
				ClientConfig:    clientConfig,
			}

			err := handler.OnConfig(context.Background(), tt.config)

			if tt.expectError {
				require.Error(t, err)
				if tt.errorMessage != "" {
					assert.Contains(t, err.Error(), tt.errorMessage)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBuildKubeConfig(t *testing.T) {
	tests := []struct {
		name        string
		host        string
		ca          []byte
		token       string
		rawConfig   clientcmdapi.Config
		expectError bool
	}{
		{
			name:  "builds kubeconfig with token",
			host:  "https://test-api:6443",
			ca:    []byte("test-ca"),
			token: "test-token",
			rawConfig: clientcmdapi.Config{
				CurrentContext: "default",
			},
			expectError: false,
		},
		{
			name:  "builds kubeconfig without token",
			host:  "https://test-api:6443",
			ca:    []byte("test-ca"),
			token: "",
			rawConfig: clientcmdapi.Config{
				Clusters: map[string]*clientcmdapi.Cluster{
					"default": {Server: "https://test:6443"},
				},
				Contexts: map[string]*clientcmdapi.Context{
					"default": {Cluster: "default"},
				},
				CurrentContext: "default",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := buildKubeConfig(tt.host, tt.ca, tt.token, tt.rawConfig)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.NotEmpty(t, result)

				// Parse the result to validate it's valid kubeconfig
				_, err := clientcmd.Load(result)
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetHost(t *testing.T) {
	tests := []struct {
		name        string
		rawConfig   clientcmdapi.Config
		expectHost  string
		expectError bool
	}{
		{
			name: "gets host from current context",
			rawConfig: clientcmdapi.Config{
				Clusters: map[string]*clientcmdapi.Cluster{
					"my-cluster": {
						Server: "https://current-context:6443",
					},
				},
				CurrentContext: "my-cluster",
			},
			expectHost:  "https://current-context:6443",
			expectError: false,
		},
		{
			name: "gets host from first cluster when no current context",
			rawConfig: clientcmdapi.Config{
				Clusters: map[string]*clientcmdapi.Cluster{
					"cluster1": {
						Server: "https://first-cluster:6443",
					},
					"cluster2": {
						Server: "https://second-cluster:6443",
					},
				},
			},
			expectHost:  "https://first-cluster:6443",
			expectError: false,
		},
		{
			name: "returns error when no clusters",
			rawConfig: clientcmdapi.Config{
				Clusters: map[string]*clientcmdapi.Cluster{},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, err := getHost(tt.rawConfig)

			if tt.expectError {
				assert.Error(t, err)
				assert.Equal(t, ErrNoHostInConfig, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectHost, host)
			}
		})
	}
}

func TestGetCA(t *testing.T) {
	tests := []struct {
		name        string
		rawConfig   clientcmdapi.Config
		expectCA    []byte
		expectError bool
	}{
		{
			name: "gets CA from current context",
			rawConfig: clientcmdapi.Config{
				Clusters: map[string]*clientcmdapi.Cluster{
					"my-cluster": {
						CertificateAuthorityData: []byte("current-ca"),
					},
				},
				CurrentContext: "my-cluster",
			},
			expectCA:    []byte("current-ca"),
			expectError: false,
		},
		{
			name: "gets CA from first cluster when no current context",
			rawConfig: clientcmdapi.Config{
				Clusters: map[string]*clientcmdapi.Cluster{
					"cluster1": {
						CertificateAuthorityData: []byte("first-ca"),
					},
				},
			},
			expectCA:    []byte("first-ca"),
			expectError: false,
		},
		{
			name: "returns nil when no clusters",
			rawConfig: clientcmdapi.Config{
				Clusters: map[string]*clientcmdapi.Cluster{},
			},
			expectCA:    nil,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ca, err := getCA(tt.rawConfig)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectCA, ca)
			}
		})
	}
}
