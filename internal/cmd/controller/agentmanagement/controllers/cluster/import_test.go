package cluster

import (
	"reflect"
	"strings"
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
				t.Helper()
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
				t.Helper()
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
				t.Helper()
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
				AgentTLSMode:              "strict",
				GarbageCollectionInterval: metav1.Duration{Duration: 10 * time.Minute},
			},
			handlerWithMocks: func(t *testing.T) importHandler {
				t.Helper()
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
				t.Helper()
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
				t.Helper()
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
				t.Helper()
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
				t.Helper()
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

func TestOnConfig_DisallowedNamespace(t *testing.T) {
	// A cluster with kubeConfigSecretNamespace set to a disallowed namespace
	// must be silently skipped during onConfig — the controller must not read
	// any secret from that namespace.
	cfg := config.Config{
		APIServerCA:  []byte("foo"),
		APIServerURL: "https://hello.world",
		AgentTLSMode: "system-store",
	}

	ctrl := gomock.NewController(t)
	secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
	// secretsCache.Get must never be called for the disallowed namespace.

	clustersCache := fake.NewMockCacheInterface[*fleet.Cluster](ctrl)
	clustersCache.EXPECT().List("", gomock.Eq(labels.Everything())).Return([]*fleet.Cluster{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "bad-cluster", Namespace: "fleet-default"},
			Spec: fleet.ClusterSpec{
				KubeConfigSecret:          "some-secret",
				KubeConfigSecretNamespace: "kube-system",
			},
		},
	}, nil)

	ih := importHandler{clustersCache: clustersCache, secretsCache: secretsCache}
	// onConfig silently skips clusters with disallowed namespaces (logs a warning
	// and continues); it does not propagate the error to the caller.
	if err := ih.onConfig(&cfg); err != nil {
		t.Errorf("unexpected error: expected nil, got %v", err)
	}
}

func TestAllowedKubeConfigSecretNamespace(t *testing.T) {
	cases := map[string]struct {
		clusterNamespace string
		fieldValue       string
		wantNS           string
		wantErrContains  string
	}{
		"empty field returns cluster namespace": {
			clusterNamespace: "fleet-default",
			fieldValue:       "",
			wantNS:           "fleet-default",
		},
		"fleet-default is allowed": {
			clusterNamespace: "fleet-local",
			fieldValue:       "fleet-default",
			wantNS:           "fleet-default",
		},
		"fleet-local is allowed": {
			clusterNamespace: "fleet-default",
			fieldValue:       "fleet-local",
			wantNS:           "fleet-local",
		},
		"cattle-fleet-system is allowed": {
			clusterNamespace: "fleet-default",
			fieldValue:       config.DefaultNamespace,
			wantNS:           config.DefaultNamespace,
		},
		"fleet-system (legacy) is allowed": {
			clusterNamespace: "fleet-default",
			fieldValue:       config.LegacyDefaultNamespace,
			wantNS:           config.LegacyDefaultNamespace,
		},
		"cattle-fleet-clusters-system is allowed": {
			clusterNamespace: "fleet-default",
			fieldValue:       "cattle-fleet-clusters-system",
			wantNS:           "cattle-fleet-clusters-system",
		},
		"fleet-clusters-system (legacy) is allowed": {
			clusterNamespace: "fleet-default",
			fieldValue:       "fleet-clusters-system",
			wantNS:           "fleet-clusters-system",
		},
		"cluster-* prefix is allowed": {
			clusterNamespace: "fleet-default",
			fieldValue:       "cluster-fleet-default-my-cluster-abc123",
			wantNS:           "cluster-fleet-default-my-cluster-abc123",
		},
		"custom workspace cluster referencing own namespace is allowed": {
			clusterNamespace: "my-workspace",
			fieldValue:       "my-workspace",
			wantNS:           "my-workspace",
		},
		"cattle-system cluster can reference own namespace": {
			clusterNamespace: "cattle-system",
			fieldValue:       "cattle-system",
			wantNS:           "cattle-system",
		},
		"kube-system is rejected": {
			clusterNamespace: "fleet-default",
			fieldValue:       "kube-system",
			wantErrContains:  "not an allowed Fleet namespace",
		},
		"cattle-system is rejected": {
			clusterNamespace: "fleet-default",
			fieldValue:       "cattle-system",
			wantErrContains:  "not an allowed Fleet namespace",
		},
		"arbitrary namespace is rejected": {
			clusterNamespace: "fleet-default",
			fieldValue:       "tenant-a-secrets",
			wantErrContains:  "not an allowed Fleet namespace",
		},
		"default namespace is rejected": {
			clusterNamespace: "fleet-default",
			fieldValue:       "default",
			wantErrContains:  "not an allowed Fleet namespace",
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			cluster := &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: c.clusterNamespace,
				},
				Spec: fleet.ClusterSpec{
					KubeConfigSecretNamespace: c.fieldValue,
				},
			}

			got, err := allowedKubeConfigSecretNamespace(cluster)
			if c.wantErrContains != "" {
				if err == nil {
					t.Errorf("expected error for namespace %q, got nil", c.fieldValue)
				} else if !strings.Contains(err.Error(), c.wantErrContains) {
					t.Errorf("expected error containing %q, got %q", c.wantErrContains, err.Error())
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error for namespace %q: %v", c.fieldValue, err)
				return
			}
			if got != c.wantNS {
				t.Errorf("expected namespace %q, got %q", c.wantNS, got)
			}
		})
	}
}

func TestLocalAgentDisabled(t *testing.T) {
	cases := []struct {
		name    string
		cluster *fleet.Cluster
		want    bool
	}{
		{
			name:    "nil labels",
			cluster: &fleet.Cluster{},
			want:    false,
		},
		{
			name: "label absent",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"name": "local"},
				},
			},
			want: false,
		},
		{
			name: "label set to true",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"name":                        "local",
						fleet.LocalAgentDisabledLabel: "true",
					},
				},
			},
			want: true,
		},
		{
			// Only the literal string "true" enables it: anything else (false,
			// empty, "1", "yes") is treated as not-disabled so users can't
			// accidentally disable the agent by toggling the label.
			name: "label set to a non-true value is treated as not disabled",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						fleet.LocalAgentDisabledLabel: "1",
					},
				},
			},
			want: false,
		},
		{
			name: "label set to false",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						fleet.LocalAgentDisabledLabel: "false",
					},
				},
			},
			want: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := localAgentDisabled(c.cluster); got != c.want {
				t.Errorf("localAgentDisabled = %v, want %v", got, c.want)
			}
		})
	}
}

func TestOnChange_LocalAgentDisabledShortCircuits(t *testing.T) {
	// A cluster carrying the local-agent-disabled label must short-circuit
	// out of OnChange before it touches any of the controllers/caches —
	// no ClientID generation, no clusters.Update, nothing.
	//
	// We assert this by leaving every field of importHandler nil; reaching
	// any of them would panic.
	cluster := &fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "local",
			Namespace: "fleet-local",
			Labels: map[string]string{
				"name":                        "local",
				fleet.LocalAgentDisabledLabel: "true",
			},
		},
		Spec: fleet.ClusterSpec{
			KubeConfigSecret: "local-cluster", // would normally trigger ClientID gen
		},
	}

	ih := importHandler{}
	got, err := ih.OnChange("fleet-local/local", cluster)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != cluster {
		t.Errorf("OnChange returned a different cluster; want short-circuit returning the input as-is")
	}
}

func TestImportCluster_LocalAgentDisabled(t *testing.T) {
	cases := map[string]struct {
		cluster    *fleet.Cluster
		status     fleet.ClusterStatus
		wantStatus fleet.ClusterStatus
	}{
		"never deployed: returns immediately with status unchanged": {
			// status.Agent.Namespace == "" && status.AgentDeployedGeneration == nil
			// → no teardown, no status mutation. Avoids reconnecting to the
			// downstream every reconcile after teardown has already run.
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "local",
					Namespace: "fleet-local",
					Labels: map[string]string{
						fleet.LocalAgentDisabledLabel: "true",
					},
				},
				Spec: fleet.ClusterSpec{
					// non-empty would normally drive the deploy path,
					// but the label must skip past it.
					KubeConfigSecret: "local-cluster",
				},
			},
			status:     fleet.ClusterStatus{},
			wantStatus: fleet.ClusterStatus{},
		},
		"previously deployed without a kubeconfig secret: clears status, skips teardown": {
			// Defensive branch: the teardown only runs when there's a
			// kubeconfig to connect with. The status is still wiped so
			// later reconciles take the early-return path above.
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "local",
					Namespace: "fleet-local",
					Labels: map[string]string{
						fleet.LocalAgentDisabledLabel: "true",
					},
				},
				Spec: fleet.ClusterSpec{
					KubeConfigSecret: "", // skips teardownLocalAgent
				},
			},
			status: fleet.ClusterStatus{
				Agent: fleet.AgentStatus{
					Namespace: "cattle-fleet-local-system",
				},
				AgentDeployedGeneration: new(int64),
				AgentConfigChanged:      true,
			},
			wantStatus: fleet.ClusterStatus{
				Agent:                   fleet.AgentStatus{},
				AgentDeployedGeneration: nil,
				AgentConfigChanged:      false,
			},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			// All importHandler fields nil — any access would panic, proving
			// the disabled path does not reach the kubeconfig/secrets/apply
			// code.
			ih := importHandler{}
			gotStatus, err := ih.importCluster(c.cluster, c.status)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(gotStatus, c.wantStatus) {
				t.Errorf("status mismatch.\nwant: %#v\ngot:  %#v", c.wantStatus, gotStatus)
			}
		})
	}
}
