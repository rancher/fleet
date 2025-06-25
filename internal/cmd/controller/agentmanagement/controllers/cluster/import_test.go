package cluster

import (
	"testing"
	"time"

	"github.com/rancher/wrangler/v3/pkg/generic/fake"
	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/labels"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestOnConfig(t *testing.T) {
	cases := map[string]struct {
		cfg              config.Config
		handlerWithMocks func(t *testing.T) importHandler
	}{
		"no clusters, no import": {
			cfg: config.Config{
				APIServerCA:  []byte("foo"),
				APIServerURL: "https://hello.world",
				AgentTLSMode: "system-store",
			},
			handlerWithMocks: func(t *testing.T) importHandler {
				ctrl := gomock.NewController(t)

				secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)

				clustersCache := fake.NewMockCacheInterface[*fleet.Cluster](ctrl)
				clustersCache.EXPECT().List("", gomock.Eq(labels.Everything())).Return(nil, nil)

				return importHandler{
					clustersCache: clustersCache,
					secretsCache:  secretsCache,
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
			handlerWithMocks: func(t *testing.T) importHandler {
				ctrl := gomock.NewController(t)

				secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
				secretsCache.EXPECT().Get(gomock.Any(), "my-kubeconfig-secret").Return(&corev1.Secret{}, nil)

				clustersCache := fake.NewMockCacheInterface[*fleet.Cluster](ctrl)
				clustersCache.EXPECT().List("", gomock.Eq(labels.Everything())).
					Return([]*fleet.Cluster{
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
					}, nil)
				clustersController := fake.NewMockControllerInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
				clustersController.EXPECT().UpdateStatus(gomock.Any()) // import triggered

				return importHandler{
					clusters:      clustersController,
					clustersCache: clustersCache,
					secretsCache:  secretsCache,
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
			handlerWithMocks: func(t *testing.T) importHandler {
				ctrl := gomock.NewController(t)

				secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
				secretsCache.EXPECT().Get(gomock.Any(), "my-kubeconfig-secret").Return(&corev1.Secret{}, nil)

				clustersCache := fake.NewMockCacheInterface[*fleet.Cluster](ctrl)
				clustersCache.EXPECT().List("", gomock.Eq(labels.Everything())).
					Return([]*fleet.Cluster{
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
					}, nil)
				clustersController := fake.NewMockControllerInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
				clustersController.EXPECT().UpdateStatus(gomock.Any()) // import triggered

				return importHandler{
					clusters:      clustersController,
					clustersCache: clustersCache,
					secretsCache:  secretsCache,
				}
			},
		},
		"non-ready config and no URL or CA in secret, do not trigger import when CA changes": {
			cfg: config.Config{
				APIServerURL:              "",
				AgentTLSMode:              "system-store",
				GarbageCollectionInterval: metav1.Duration{Duration: 10 * time.Minute},
			},
			handlerWithMocks: func(t *testing.T) importHandler {
				ctrl := gomock.NewController(t)

				secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
				secretsCache.EXPECT().Get(gomock.Any(), "my-kubeconfig-secret").Return(&corev1.Secret{}, nil)

				clustersCache := fake.NewMockCacheInterface[*fleet.Cluster](ctrl)
				clustersCache.EXPECT().List("", gomock.Eq(labels.Everything())).
					Return([]*fleet.Cluster{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "cluster",
								Namespace: "fleet-default",
							},
							Spec: fleet.ClusterSpec{
								KubeConfigSecret: "my-kubeconfig-secret",
							},
							Status: fleet.ClusterStatus{
								APIServerURL:              "",
								APIServerCAHash:           "",
								AgentTLSMode:              "system-store",
								GarbageCollectionInterval: &metav1.Duration{Duration: 10 * time.Minute},
							},
						},
					}, nil)
				clustersController := fake.NewMockControllerInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
				clustersController.EXPECT().UpdateStatus(gomock.Any()).Times(0) // import not triggered

				return importHandler{
					clusters:      clustersController,
					clustersCache: clustersCache,
					secretsCache:  secretsCache,
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
			handlerWithMocks: func(t *testing.T) importHandler {
				ctrl := gomock.NewController(t)

				secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
				secretsCache.EXPECT().Get(gomock.Any(), "my-kubeconfig-secret").Return(&corev1.Secret{}, nil)

				clustersCache := fake.NewMockCacheInterface[*fleet.Cluster](ctrl)
				clustersCache.EXPECT().List("", gomock.Eq(labels.Everything())).
					Return([]*fleet.Cluster{
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
					}, nil)
				clustersController := fake.NewMockControllerInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
				clustersController.EXPECT().UpdateStatus(gomock.Any()) // import triggered

				return importHandler{
					clusters:      clustersController,
					clustersCache: clustersCache,
					secretsCache:  secretsCache,
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
			handlerWithMocks: func(t *testing.T) importHandler {
				ctrl := gomock.NewController(t)

				secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
				secretsCache.EXPECT().Get(gomock.Any(), "my-kubeconfig-secret").Return(&corev1.Secret{}, nil)

				clustersCache := fake.NewMockCacheInterface[*fleet.Cluster](ctrl)
				clustersCache.EXPECT().List("", gomock.Eq(labels.Everything())).
					Return([]*fleet.Cluster{
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
					}, nil)
				clustersController := fake.NewMockControllerInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
				clustersController.EXPECT().UpdateStatus(gomock.Any()) // import triggered

				return importHandler{
					clusters:      clustersController,
					clustersCache: clustersCache,
					secretsCache:  secretsCache,
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
			handlerWithMocks: func(t *testing.T) importHandler {
				ctrl := gomock.NewController(t)

				clustersCache := fake.NewMockCacheInterface[*fleet.Cluster](ctrl)
				clustersCache.EXPECT().List("", gomock.Eq(labels.Everything())).
					Return([]*fleet.Cluster{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "cluster",
								Namespace: "fleet-default",
							},
							Spec: fleet.ClusterSpec{
								KubeConfigSecret: "my-kubeconfig-secret",
							},
							Status: fleet.ClusterStatus{
								APIServerURL:              "https://hello.secret.world",
								APIServerCAHash:           hashStatusField("secret-foo"),
								AgentTLSMode:              "system-store",
								GarbageCollectionInterval: &metav1.Duration{Duration: 10 * time.Minute},
							},
						},
					}, nil)
				secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
				secretsCache.EXPECT().Get(gomock.Any(), "my-kubeconfig-secret").Return(&corev1.Secret{
					Data: map[string][]byte{
						"apiServerURL": []byte("https://hello.secret.world"),
						"apiServerCA":  []byte(hashStatusField("secret-foo")),
					},
				}, nil)

				// No UpdateStatus expected

				return importHandler{
					clustersCache: clustersCache,
					secretsCache:  secretsCache,
				}
			},
		},
		"URL and CA in secret, trigger import when agent TLS mode changes": {
			cfg: config.Config{
				APIServerCA:               []byte("foo"),
				APIServerURL:              "https://hello.world",
				AgentTLSMode:              "strict",
				GarbageCollectionInterval: metav1.Duration{Duration: 10 * time.Minute},
			},
			handlerWithMocks: func(t *testing.T) importHandler {
				ctrl := gomock.NewController(t)

				clustersCache := fake.NewMockCacheInterface[*fleet.Cluster](ctrl)
				clustersCache.EXPECT().List("", gomock.Eq(labels.Everything())).
					Return([]*fleet.Cluster{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "cluster",
								Namespace: "fleet-default",
							},
							Spec: fleet.ClusterSpec{
								KubeConfigSecret: "my-kubeconfig-secret",
							},
							Status: fleet.ClusterStatus{
								APIServerURL:              "https://hello.secret.world",
								APIServerCAHash:           hashStatusField("secret-foo"),
								AgentTLSMode:              "system-store",
								GarbageCollectionInterval: &metav1.Duration{Duration: 10 * time.Minute},
							},
						},
					}, nil)
				secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
				secretsCache.EXPECT().Get(gomock.Any(), "my-kubeconfig-secret").Return(&corev1.Secret{
					Data: map[string][]byte{
						"apiServerURL": []byte("https://hello.secret.world"),
						"apiServerCA":  []byte(hashStatusField("secret-foo")),
					},
				}, nil)

				clustersController := fake.NewMockControllerInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
				clustersController.EXPECT().UpdateStatus(gomock.Any()) // import triggered

				return importHandler{
					clusters:      clustersController,
					clustersCache: clustersCache,
					secretsCache:  secretsCache,
				}
			},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ih := c.handlerWithMocks(t)

			err := ih.onConfig(&c.cfg)
			if err != nil {
				t.Errorf("unexpected error: expected nil, got %v", err)
			}

		})
	}
}
