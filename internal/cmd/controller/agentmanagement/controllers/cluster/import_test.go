package cluster

import (
	"testing"
	"time"

	"github.com/rancher/wrangler/v3/pkg/generic/fake"
	"go.uber.org/mock/gomock"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestOnConfig(t *testing.T) {
	cases := map[string]struct {
		cfg              config.Config
		handlerWithMocks func() importHandler
	}{
		"no clusters, no import": {
			cfg: config.Config{
				APIServerCA:  []byte("foo"),
				APIServerURL: "https://hello.world",
				AgentTLSMode: "system-store",
			},
			handlerWithMocks: func() importHandler {
				ctrl := gomock.NewController(t)

				secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)

				clustersController := fake.NewMockControllerInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
				clustersController.EXPECT().List("", metav1.ListOptions{}).Return(&fleet.ClusterList{}, nil)

				return importHandler{
					clusters: clustersController,
					secrets:  secretsCache,
				}
			},
		},
		"no URL or CA in secret, do not trigger import when URL changes": {
			cfg: config.Config{
				APIServerCA:               []byte("foo"),
				APIServerURL:              "https://hello.new.world",
				AgentTLSMode:              "system-store",
				GarbageCollectionInterval: metav1.Duration{Duration: 10 * time.Minute},
			},
			handlerWithMocks: func() importHandler {
				ctrl := gomock.NewController(t)

				secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
				secretsCache.EXPECT().Get(gomock.Any(), "my-kubeconfig-secret").Return(&corev1.Secret{}, nil)

				clustersController := fake.NewMockControllerInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
				clustersController.EXPECT().List("", metav1.ListOptions{}).
					Return(&fleet.ClusterList{
						Items: []fleet.Cluster{
							{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "cluster",
									Namespace: "fleet-default",
								},
								Spec: fleet.ClusterSpec{
									KubeConfigSecret: "my-kubeconfig-secret",
								},
								Status: fleet.ClusterStatus{
									APIServerURL:              "https://hello.world",
									APIServerCAHash:           hashStatusField("foo"),
									AgentTLSMode:              "system-store",
									GarbageCollectionInterval: &metav1.Duration{Duration: 10 * time.Minute},
								},
							},
						},
					}, nil)

				clustersController.EXPECT().UpdateStatus(gomock.Any()) // import triggered

				return importHandler{
					clusters: clustersController,
					secrets:  secretsCache,
				}
			},
		},
		"no URL or CA in secret, do not trigger import when CA changes": {
			cfg: config.Config{
				APIServerCA:               []byte("new-foo"),
				APIServerURL:              "https://hello.world",
				AgentTLSMode:              "system-store",
				GarbageCollectionInterval: metav1.Duration{Duration: 10 * time.Minute},
			},
			handlerWithMocks: func() importHandler {
				ctrl := gomock.NewController(t)

				secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
				secretsCache.EXPECT().Get(gomock.Any(), "my-kubeconfig-secret").Return(&corev1.Secret{}, nil)

				clustersController := fake.NewMockControllerInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
				clustersController.EXPECT().List("", metav1.ListOptions{}).
					Return(&fleet.ClusterList{
						Items: []fleet.Cluster{
							{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "cluster",
									Namespace: "fleet-default",
								},
								Spec: fleet.ClusterSpec{
									KubeConfigSecret: "my-kubeconfig-secret",
								},
								Status: fleet.ClusterStatus{
									APIServerURL:              "https://hello.world",
									APIServerCAHash:           hashStatusField("foo"),
									AgentTLSMode:              "system-store",
									GarbageCollectionInterval: &metav1.Duration{Duration: 10 * time.Minute},
								},
							},
						},
					}, nil)

				clustersController.EXPECT().UpdateStatus(gomock.Any()) // import triggered

				return importHandler{
					clusters: clustersController,
					secrets:  secretsCache,
				}
			},
		},
		"no URL or CA in secret, trigger import when agent TLS mode changes": {
			cfg: config.Config{
				APIServerCA:               []byte("foo"),
				APIServerURL:              "https://hello.world",
				AgentTLSMode:              "strict",
				GarbageCollectionInterval: metav1.Duration{Duration: 10 * time.Minute},
			},
			handlerWithMocks: func() importHandler {
				ctrl := gomock.NewController(t)

				secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
				secretsCache.EXPECT().Get(gomock.Any(), "my-kubeconfig-secret").Return(&corev1.Secret{}, nil)

				clustersController := fake.NewMockControllerInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
				clustersController.EXPECT().List("", metav1.ListOptions{}).
					Return(&fleet.ClusterList{
						Items: []fleet.Cluster{
							{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "cluster",
									Namespace: "fleet-default",
								},
								Spec: fleet.ClusterSpec{
									KubeConfigSecret: "my-kubeconfig-secret",
								},
								Status: fleet.ClusterStatus{
									APIServerURL:              "https://hello.world",
									APIServerCAHash:           hashStatusField("foo"),
									AgentTLSMode:              "system-store",
									GarbageCollectionInterval: &metav1.Duration{Duration: 10 * time.Minute},
								},
							},
						},
					}, nil)

				clustersController.EXPECT().UpdateStatus(gomock.Any()) // import triggered

				return importHandler{
					clusters: clustersController,
					secrets:  secretsCache,
				}
			},
		},
		"no URL or CA in secret, trigger import when agent garbage collection interval changes": {
			cfg: config.Config{
				APIServerCA:               []byte("foo"),
				APIServerURL:              "https://hello.world",
				AgentTLSMode:              "system-store",
				GarbageCollectionInterval: metav1.Duration{Duration: 5 * time.Minute},
			},
			handlerWithMocks: func() importHandler {
				ctrl := gomock.NewController(t)

				secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
				secretsCache.EXPECT().Get(gomock.Any(), "my-kubeconfig-secret").Return(&corev1.Secret{}, nil)

				clustersController := fake.NewMockControllerInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
				clustersController.EXPECT().List("", metav1.ListOptions{}).
					Return(&fleet.ClusterList{
						Items: []fleet.Cluster{
							{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "cluster",
									Namespace: "fleet-default",
								},
								Spec: fleet.ClusterSpec{
									KubeConfigSecret: "my-kubeconfig-secret",
								},
								Status: fleet.ClusterStatus{
									APIServerURL:              "https://hello.world",
									APIServerCAHash:           hashStatusField("foo"),
									AgentTLSMode:              "system-store",
									GarbageCollectionInterval: &metav1.Duration{Duration: 10 * time.Minute},
								},
							},
						},
					}, nil)

				clustersController.EXPECT().UpdateStatus(gomock.Any()) // import triggered

				return importHandler{
					clusters: clustersController,
					secrets:  secretsCache,
				}
			},
		},
		"URL and CA in secret, do not trigger import when only URL or CA changes": {
			cfg: config.Config{
				APIServerCA:               []byte("new-foo"),
				APIServerURL:              "https://hello.new.world",
				AgentTLSMode:              "system-store",
				GarbageCollectionInterval: metav1.Duration{Duration: 10 * time.Minute},
			},
			handlerWithMocks: func() importHandler {
				ctrl := gomock.NewController(t)

				clustersController := fake.NewMockControllerInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
				clustersController.EXPECT().List("", metav1.ListOptions{}).
					Return(&fleet.ClusterList{
						Items: []fleet.Cluster{
							{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "cluster",
									Namespace: "fleet-default",
								},
								Spec: fleet.ClusterSpec{
									KubeConfigSecret: "my-kubeconfig-secret",
								},
								Status: fleet.ClusterStatus{
									APIServerURL:              "https://hello.world",
									APIServerCAHash:           hashStatusField("foo"),
									AgentTLSMode:              "system-store",
									GarbageCollectionInterval: &metav1.Duration{Duration: 10 * time.Minute},
								},
							},
						},
					}, nil)

				secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
				secretsCache.EXPECT().Get(gomock.Any(), "my-kubeconfig-secret").Return(&corev1.Secret{
					Data: map[string][]byte{
						"apiServerURL": []byte("https://hello.new.world"),
						"apiServerCA":  []byte(hashStatusField("foo-new")),
					},
				}, nil)

				// No UpdateStatus expected

				return importHandler{
					clusters: clustersController,
					secrets:  secretsCache,
				}
			},
		},
		"URL and CA in secret, trigger import when agent TLS mode changes": {
			cfg: config.Config{
				APIServerCA:               []byte("new-foo"),
				APIServerURL:              "https://hello.new.world",
				AgentTLSMode:              "strict",
				GarbageCollectionInterval: metav1.Duration{Duration: 10 * time.Minute},
			},
			handlerWithMocks: func() importHandler {
				ctrl := gomock.NewController(t)

				clustersController := fake.NewMockControllerInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
				clustersController.EXPECT().List("", metav1.ListOptions{}).
					Return(&fleet.ClusterList{
						Items: []fleet.Cluster{
							{
								ObjectMeta: metav1.ObjectMeta{
									Name:      "cluster",
									Namespace: "fleet-default",
								},
								Spec: fleet.ClusterSpec{
									KubeConfigSecret: "my-kubeconfig-secret",
								},
								Status: fleet.ClusterStatus{
									APIServerURL:              "https://hello.world",
									APIServerCAHash:           hashStatusField("foo"),
									AgentTLSMode:              "system-store",
									GarbageCollectionInterval: &metav1.Duration{Duration: 10 * time.Minute},
								},
							},
						},
					}, nil)

				secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
				secretsCache.EXPECT().Get(gomock.Any(), "my-kubeconfig-secret").Return(&corev1.Secret{
					Data: map[string][]byte{
						"apiServerURL": []byte("https://hello.new.world"),
						"apiServerCA":  []byte(hashStatusField("foo-new")),
					},
				}, nil)

				clustersController.EXPECT().UpdateStatus(gomock.Any()) // import triggered

				return importHandler{
					clusters: clustersController,
					secrets:  secretsCache,
				}
			},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ih := c.handlerWithMocks()

			err := ih.onConfig(&c.cfg)
			if err != nil {
				t.Errorf("unexpected error: expected nil, got %v", err)
			}

		})
	}
}
