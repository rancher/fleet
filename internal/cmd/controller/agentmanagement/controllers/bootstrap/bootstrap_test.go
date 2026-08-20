//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/wrangler_apply_mock.go -package=mocks github.com/rancher/wrangler/v3/pkg/apply Apply
//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/clientcmd_config.go -package=mocks k8s.io/client-go/tools/clientcmd ClientConfig
package bootstrap

import (
	"reflect"
	"testing"

	fleetconfig "github.com/rancher/fleet/internal/config"
	mocks "github.com/rancher/fleet/internal/mocks"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/generic/fake"
	gomock "go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestOnConfig_AppliesClusterLabels(t *testing.T) {
	cases := []struct {
		name           string
		configLabels   map[string]string
		expectedLabels map[string]string
	}{
		{
			name: "no added labels",
			expectedLabels: map[string]string{
				"name": "local",
			},
		},
		{
			name: "with added labels",
			configLabels: map[string]string{
				"region":            "eu-west-1",
				"random-hash-12345": "enabled",
			},
			expectedLabels: map[string]string{
				"name":              "local",
				"region":            "eu-west-1",
				"random-hash-12345": "enabled",
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {

			inputConfig := &fleetconfig.Config{
				Bootstrap: fleetconfig.Bootstrap{
					Namespace:     "bootstrap-ns", // not empty
					ClusterLabels: c.configLabels,
				},
			}

			ctrl := gomock.NewController(t)
			mockApply := mocks.NewMockApply(ctrl)

			var foundCluster *fleet.Cluster

			mockApply.EXPECT().WithNoDeleteGVK(gomock.Any()).Return(mockApply)
			mockApply.EXPECT().ApplyObjects(gomock.Any()).DoAndReturn(
				func(objs ...runtime.Object) error {
					for _, o := range objs {
						cluster, ok := o.(*fleet.Cluster)
						if !ok {
							continue
						}
						foundCluster = cluster
					}

					return nil
				},
			)

			// for handler.buildSecret
			mockClientCfg := mocks.NewMockClientConfig(ctrl)
			mockClientCfg.EXPECT().RawConfig().Return(clientcmdapi.Config{
				CurrentContext: "foo",
				Clusters: map[string]*clientcmdapi.Cluster{
					"foo": {
						CertificateAuthorityData: []byte("t0pS3cr37"), // for handler.getCA(rawConfig)
						Server:                   "foo-server",        // for handler.getHost(rawConfig)
					},
				},
			}, nil)

			sac := fake.NewMockCacheInterface[*corev1.ServiceAccount](ctrl)
			sac.EXPECT().Get(gomock.Any(), gomock.Any()).Return(&corev1.ServiceAccount{
				Secrets: []corev1.ObjectReference{
					{
						Name: "token-secret", // for handler.getToken()
					},
				},
			}, nil)

			sc := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
			sc.EXPECT().Get(gomock.Any(), gomock.Any()).Return(&corev1.Secret{
				Data: map[string][]byte{
					corev1.ServiceAccountTokenKey: []byte("my-token"), // for handler.getToken()
				},
			}, nil)

			dc := fake.NewMockCacheInterface[*appsv1.Deployment](ctrl)
			dc.EXPECT().Get(gomock.Any(), gomock.Any()).Return(&appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Tolerations: []corev1.Toleration{},
						},
					},
				},
			}, nil)

			h := handler{
				apply:               mockApply,
				cfg:                 mockClientCfg,
				deploymentsCache:    dc,
				secretsCache:        sc,
				serviceAccountCache: sac,
				systemNamespace:     "system-ns", // non-empty (needed by handler.getToken())
			}

			err := h.OnConfig(inputConfig)

			if err != nil {
				t.Fatalf("OnConfig returned an unexpected error: %v", err)
			}

			if foundCluster == nil {
				t.Fatalf("did not find expected cluster in applied objects")
			}

			if foundCluster.Name != "local" {
				t.Errorf("Expected Cluster name 'local', got %q", foundCluster.Name)
			}

			if !reflect.DeepEqual(foundCluster.Labels, c.expectedLabels) {
				t.Errorf("Labels mismatch on applied Cluster object.\nExpected: %v\nGot: %v", c.expectedLabels, foundCluster.Labels)
			}
		})
	}
}
