//go:generate mockgen -destination=../../../../mocks/client_getter_mock.go -package=mocks -mock_names=GetterInterface=MockClientGetter github.com/rancher/fleet/internal/client GetterInterface
//go:generate mockgen -destination=../../../../mocks/fleet_controllers_mock.go -package=mocks -mock_names=Interface=MockFleetControllers github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1 Interface
//go:generate mockgen -destination=../../../../mocks/wrangler_core_mock.go -package=mocks -mock_names=Interface=MockWranglerCore github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1 Interface
package agent_test

import (
	"context"
	"testing"

	"github.com/rancher/fleet/internal/client"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/agent"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/mocks"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/generic/fake"
	"go.uber.org/mock/gomock"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

func TestAgentWithConfig(t *testing.T) {
	tests := []struct {
		name                string
		imagePullSecrets    []corev1.LocalObjectReference
		propagate           bool
		expectSecretsInObj  bool
		expectSecretsInDepl bool
	}{
		{
			name: "with ImagePullSecrets and propagation enabled: secrets should be copied",
			imagePullSecrets: []corev1.LocalObjectReference{
				{Name: "my-pull-secret"},
			},
			propagate:           true,
			expectSecretsInObj:  true,
			expectSecretsInDepl: true,
		},
		{
			name: "with ImagePullSecrets and propagation disabled: no secrets should be copied",
			imagePullSecrets: []corev1.LocalObjectReference{
				{Name: "my-pull-secret"},
			},
			propagate:           false,
			expectSecretsInObj:  false,
			expectSecretsInDepl: true, // no propagation, but the secret should still be referenced by the agent deployment
		},
		{
			name:               "without ImagePullSecrets: no secrets should be copied",
			imagePullSecrets:   []corev1.LocalObjectReference{},
			propagate:          true, // should not matter
			expectSecretsInObj: false,
		},
		{
			name:               "nil ImagePullSecrets: no secrets should be copied",
			imagePullSecrets:   nil,
			propagate:          true, // should not matter
			expectSecretsInObj: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			// ensure leader election env is set so NewLeaderElectionOptionsWithPrefix doesn't error
			t.Setenv("FLEET_AGENT_ELECTION_LEASE_DURATION", "15s")
			t.Setenv("FLEET_AGENT_ELECTION_RENEW_DEADLINE", "10s")
			t.Setenv("FLEET_AGENT_ELECTION_RETRY_PERIOD", "2s")

			agentNamespace := "cluster-fleet-test-ns"
			controllerNamespace := "cattle-fleet-system"
			agentScope := ""
			tokenName := "test-token"

			// Set up mock clients
			crtClient := fake.NewMockControllerInterface[*fleet.ClusterRegistrationToken, *fleet.ClusterRegistrationTokenList](ctrl)
			clusterClient := fake.NewMockControllerInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
			secretClient := fake.NewMockControllerInterface[*corev1.Secret, *corev1.SecretList](ctrl)
			configMapClient := fake.NewMockControllerInterface[*corev1.ConfigMap, *corev1.ConfigMapList](ctrl)

			// Mock the ClusterRegistrationToken to return a secret name
			mockToken := &fleet.ClusterRegistrationToken{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tokenName,
					Namespace: controllerNamespace,
					UID:       "test-uid",
				},
				Status: fleet.ClusterRegistrationTokenStatus{
					SecretName: "import-token-secret",
				},
			}

			// Mock the import token secret with required data
			importTokenSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "import-token-secret",
					Namespace: controllerNamespace,
				},
				Data: map[string][]byte{
					"values": []byte(`token: test-token-value
systemRegistrationNamespace: cattle-fleet-clusters-system`),
				},
			}

			managerConfig := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      config.ManagerConfigName,
					Namespace: controllerNamespace,
				},
				Data: map[string]string{
					"config": `{
						"agentImage": "rancher/fleet-agent:test",
						"agentImagePullPolicy": "Always",
						"agentCheckinInterval": "20m",
						"systemDefaultRegistry": "",
						"imagePullSecrets": []
					}`,
				},
			}

			// Mock the pull secrets (if provided)
			if tc.propagate {
				for _, ps := range tc.imagePullSecrets {
					pullSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      ps.Name,
							Namespace: controllerNamespace,
						},
						Type: corev1.SecretTypeDockerConfigJson,
						Data: map[string][]byte{
							".dockerconfigjson": []byte(`{"auths":{"registry.example.com":{"auth":"dGVzdDp0ZXN0"}}}`),
						},
					}
					secretClient.EXPECT().Get(controllerNamespace, ps.Name, gomock.Any()).Return(pullSecret, nil)
				}
			}

			crtClient.EXPECT().Get(controllerNamespace, tokenName, gomock.Any()).Return(mockToken, nil).AnyTimes()
			secretClient.EXPECT().Get(controllerNamespace, "import-token-secret", gomock.Any()).Return(importTokenSecret, nil)
			configMapClient.EXPECT().Get(controllerNamespace, config.ManagerConfigName, gomock.Any()).Return(managerConfig, nil).AnyTimes()

			// Mock watcher (not used in this code path since secretName is already set)
			mockWatcher := watch.NewFake()
			crtClient.EXPECT().Watch(gomock.Any(), gomock.Any()).Return(mockWatcher, nil).AnyTimes()
			crtClient.EXPECT().List(gomock.Any(), gomock.Any()).Return(
				&fleet.ClusterRegistrationTokenList{
					Items: []fleet.ClusterRegistrationToken{*mockToken},
				}, nil).
				AnyTimes()

			mockFleetControllers := mocks.NewMockFleetControllers(ctrl)
			mockFleetControllers.EXPECT().ClusterRegistrationToken().Return(crtClient).AnyTimes()
			mockFleetControllers.EXPECT().Cluster().Return(clusterClient).AnyTimes()

			mockCoreControllers := mocks.NewMockWranglerCore(ctrl)
			mockCoreControllers.EXPECT().Secret().Return(secretClient).AnyTimes()
			mockCoreControllers.EXPECT().ConfigMap().Return(configMapClient).AnyTimes()

			// Create a mock client that will be returned by the getter
			mockClient := &client.Client{
				Namespace: controllerNamespace,
				Core:      mockCoreControllers,
				Fleet:     mockFleetControllers,
			}

			mockClientGetter := mocks.NewMockClientGetter(ctrl)
			mockClientGetter.EXPECT().Get().Return(mockClient, nil).AnyTimes()
			mockClientGetter.EXPECT().GetNamespace().Return(controllerNamespace).AnyTimes()

			opts := &agent.Options{
				ManifestOptions: agent.ManifestOptions{
					ImagePullSecrets:     tc.imagePullSecrets,
					PropagatePullSecrets: tc.propagate,
				},
			}

			objs, err := agent.AgentWithConfig(
				ctx,
				agentNamespace,
				controllerNamespace,
				agentScope,
				mockClientGetter,
				tokenName,
				opts,
			)
			if err != nil {
				t.Fatalf("AgentWithConfig failed: %v", err)
			}

			// Check that objects created for the agent include image pull secrets, if expected
			secretCount := 0
			for _, obj := range objs {
				if secret, ok := obj.(*corev1.Secret); ok {
					for _, ps := range tc.imagePullSecrets {
						if secret.Name == ps.Name && secret.Namespace == agentNamespace {
							secretCount++
						}
					}
				}
			}

			if tc.expectSecretsInObj && secretCount == 0 {
				t.Error("Expected ImagePullSecrets to be copied to objects, but found none")
			}

			if !tc.expectSecretsInObj && secretCount > 0 {
				t.Errorf("Expected no ImagePullSecrets in objects, but found %d", secretCount)
			}

			var deployment *appsv1.Deployment
			for _, obj := range objs {
				if depl, ok := obj.(*appsv1.Deployment); ok {
					deployment = depl
					break
				}
			}

			if deployment == nil {
				t.Fatal("Expected non-nil Fleet agent deployment")
			}

			if tc.expectSecretsInDepl && len(deployment.Spec.Template.Spec.ImagePullSecrets) == 0 {
				t.Error("Expected ImagePullSecrets to be referenced in agent deployment, but found none")
			}

			if !tc.expectSecretsInDepl && len(deployment.Spec.Template.Spec.ImagePullSecrets) > 0 {
				t.Errorf(
					"Expected no ImagePullSecrets referenced in agent deployment, but found %d: %v",
					len(deployment.Spec.Template.Spec.ImagePullSecrets),
					deployment.Spec.Template.Spec.ImagePullSecrets,
				)
			}
		})
	}
}
