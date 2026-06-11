package cluster

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rancher/wrangler/v3/pkg/generic/fake"
	"go.uber.org/mock/gomock"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/agent"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/scheduling"
	fleetns "github.com/rancher/fleet/internal/cmd/controller/namespace"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// localBootstrapNamespace is the bootstrap namespace the local cluster lives in
// throughout these tests; it must match the cluster objects' Namespace for
// isLocalCluster to recognize them.
const localBootstrapNamespace = "fleet-local"

// setLocalBootstrapConfig points config.Get().Bootstrap.Namespace at
// localBootstrapNamespace so isLocalCluster can resolve the local cluster.
func setLocalBootstrapConfig(t *testing.T) {
	t.Helper()
	config.Set(&config.Config{Bootstrap: config.Bootstrap{Namespace: localBootstrapNamespace}})
}

// agentResourceObjects returns one object of every kind that the deploy path
// creates for a fleet-agent in namespace, named exactly as agent.Manifest and
// deleteAgentResources expect.
func agentResourceObjects(namespace string) []runtime.Object {
	return []runtime.Object{
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: config.AgentConfigName}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: config.AgentBootstrapConfigName}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: config.AgentConfigName}},
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: agent.DefaultName}},
		&networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "default-allow-all"}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: config.AgentConfigName}},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: config.AgentConfigName}},
		&policyv1.PodDisruptionBudget{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: scheduling.FleetAgentPodDisruptionBudgetName}},
		&schedulingv1.PriorityClass{ObjectMeta: metav1.ObjectMeta{Name: scheduling.FleetAgentPriorityClassName}},
		&rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: names.SafeConcatName(namespace, agent.DefaultName, "role")}},
		&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: names.SafeConcatName(namespace, agent.DefaultName, "role", "binding")}},
	}
}

// assertAgentResourcesGone fails the test if any agent resource still exists in
// namespace (or, for the cluster-scoped ones, with names derived from it).
func assertAgentResourcesGone(ctx context.Context, t *testing.T, kc kubernetes.Interface, namespace string) {
	t.Helper()
	checkGone := func(kind string, err error) {
		if !apierrors.IsNotFound(err) {
			t.Errorf("%s in %q: expected NotFound, got %v", kind, namespace, err)
		}
	}

	_, err := kc.CoreV1().Secrets(namespace).Get(ctx, config.AgentConfigName, metav1.GetOptions{})
	checkGone("agent secret", err)
	_, err = kc.CoreV1().Secrets(namespace).Get(ctx, config.AgentBootstrapConfigName, metav1.GetOptions{})
	checkGone("bootstrap secret", err)
	_, err = kc.CoreV1().ConfigMaps(namespace).Get(ctx, config.AgentConfigName, metav1.GetOptions{})
	checkGone("configmap", err)
	_, err = kc.CoreV1().ServiceAccounts(namespace).Get(ctx, agent.DefaultName, metav1.GetOptions{})
	checkGone("serviceaccount", err)
	_, err = kc.NetworkingV1().NetworkPolicies(namespace).Get(ctx, "default-allow-all", metav1.GetOptions{})
	checkGone("networkpolicy", err)
	_, err = kc.AppsV1().Deployments(namespace).Get(ctx, config.AgentConfigName, metav1.GetOptions{})
	checkGone("deployment", err)
	_, err = kc.AppsV1().StatefulSets(namespace).Get(ctx, config.AgentConfigName, metav1.GetOptions{})
	checkGone("statefulset", err)
	_, err = kc.PolicyV1().PodDisruptionBudgets(namespace).Get(ctx, scheduling.FleetAgentPodDisruptionBudgetName, metav1.GetOptions{})
	checkGone("poddisruptionbudget", err)
	_, err = kc.SchedulingV1().PriorityClasses().Get(ctx, scheduling.FleetAgentPriorityClassName, metav1.GetOptions{})
	checkGone("priorityclass", err)
	_, err = kc.RbacV1().ClusterRoles().Get(ctx, names.SafeConcatName(namespace, agent.DefaultName, "role"), metav1.GetOptions{})
	checkGone("clusterrole", err)
	_, err = kc.RbacV1().ClusterRoleBindings().Get(ctx, names.SafeConcatName(namespace, agent.DefaultName, "role", "binding"), metav1.GetOptions{})
	checkGone("clusterrolebinding", err)
}

func TestDeleteAgentResources(t *testing.T) {
	const ns = "cattle-fleet-system"
	kc := kubernetesfake.NewSimpleClientset(agentResourceObjects(ns)...)
	ih := importHandler{ctx: context.Background()}

	// Every agent resource kind — including the ServiceAccount, ConfigMap,
	// NetworkPolicy and ClusterRole/Binding the old deploy-path deleter missed —
	// must be removed.
	if err := ih.deleteAgentResources(kc, ns); err != nil {
		t.Fatalf("deleteAgentResources: %v", err)
	}
	assertAgentResourcesGone(context.Background(), t, kc, ns)

	// Idempotent: a second pass over already-deleted resources must not error.
	if err := ih.deleteAgentResources(kc, ns); err != nil {
		t.Fatalf("deleteAgentResources (idempotent call): %v", err)
	}
}

func TestCleanupLeftoverDefaultNamespace(t *testing.T) {
	regNS := fleetns.SystemRegistrationNamespace(config.DefaultNamespace)
	ctx := context.Background()

	t.Run("system namespace is the default: nothing is touched", func(t *testing.T) {
		kc := kubernetesfake.NewSimpleClientset(
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: regNS}},
		)
		ih := importHandler{ctx: ctx, systemNamespace: config.DefaultNamespace}

		if err := ih.cleanupLeftoverDefaultNamespace(kc); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, err := kc.CoreV1().Namespaces().Get(ctx, regNS, metav1.GetOptions{}); err != nil {
			t.Errorf("registration namespace should have been left untouched, got %v", err)
		}
	})

	t.Run("customized system namespace cleans the leftover agent and registration namespace", func(t *testing.T) {
		objs := append(agentResourceObjects(config.DefaultNamespace),
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: config.DefaultNamespace}},
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: regNS}},
		)
		kc := kubernetesfake.NewSimpleClientset(objs...)
		ih := importHandler{ctx: ctx, systemNamespace: "cattle-fleet-local-system"}

		if err := ih.cleanupLeftoverDefaultNamespace(kc); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertAgentResourcesGone(ctx, t, kc, config.DefaultNamespace)
		if _, err := kc.CoreV1().Namespaces().Get(ctx, regNS, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Errorf("registration namespace should have been deleted, got %v", err)
		}
		// The default namespace itself must be preserved.
		if _, err := kc.CoreV1().Namespaces().Get(ctx, config.DefaultNamespace, metav1.GetOptions{}); err != nil {
			t.Errorf("default namespace should have been kept, got %v", err)
		}
	})

	t.Run("customized system namespace with no default namespace still drops the registration namespace", func(t *testing.T) {
		kc := kubernetesfake.NewSimpleClientset(
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: regNS}},
		)
		ih := importHandler{ctx: ctx, systemNamespace: "cattle-fleet-local-system"}

		if err := ih.cleanupLeftoverDefaultNamespace(kc); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, err := kc.CoreV1().Namespaces().Get(ctx, regNS, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Errorf("registration namespace should have been deleted, got %v", err)
		}
	})
}

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

// localCluster returns a Cluster object recognized as the management (local)
// cluster by isLocalCluster, carrying the given labels.
func localCluster(labels map[string]string) *fleet.Cluster {
	return &fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fleet.LocalClusterName,
			Namespace: localBootstrapNamespace,
			Labels:    labels,
		},
	}
}

func TestLocalAgentDisabled(t *testing.T) {
	setLocalBootstrapConfig(t)

	cases := []struct {
		name    string
		cluster *fleet.Cluster
		want    bool
	}{
		{
			name:    "nil labels",
			cluster: localCluster(nil),
			want:    false,
		},
		{
			name:    "label absent",
			cluster: localCluster(map[string]string{"name": "local"}),
			want:    false,
		},
		{
			name: "label set to true on the local cluster",
			cluster: localCluster(map[string]string{
				"name":                        "local",
				fleet.LocalAgentDisabledLabel: "true",
			}),
			want: true,
		},
		{
			// The label must only ever take effect on the management cluster:
			// a downstream cluster carrying it (wrong namespace) must not be
			// treated as disabled, otherwise its agent would be torn down.
			name: "label set to true on a non-local cluster (wrong namespace)",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fleet.LocalClusterName,
					Namespace: "some-downstream-ns",
					Labels: map[string]string{
						fleet.LocalAgentDisabledLabel: "true",
					},
				},
			},
			want: false,
		},
		{
			name: "label set to true on a non-local cluster (wrong name)",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "downstream",
					Namespace: localBootstrapNamespace,
					Labels: map[string]string{
						fleet.LocalAgentDisabledLabel: "true",
					},
				},
			},
			want: false,
		},
		{
			// Only the literal string "true" enables it: anything else (false,
			// empty, "1", "yes") is treated as not-disabled so users can't
			// accidentally disable the agent by toggling the label.
			name:    "label set to a non-true value is treated as not disabled",
			cluster: localCluster(map[string]string{fleet.LocalAgentDisabledLabel: "1"}),
			want:    false,
		},
		{
			name:    "label set to false",
			cluster: localCluster(map[string]string{fleet.LocalAgentDisabledLabel: "false"}),
			want:    false,
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

func TestIsLocalCluster(t *testing.T) {
	setLocalBootstrapConfig(t)

	cases := []struct {
		name    string
		cluster *fleet.Cluster
		want    bool
	}{
		{
			name:    "correct name and namespace",
			cluster: localCluster(nil),
			want:    true,
		},
		{
			name: "correct name, wrong namespace",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: fleet.LocalClusterName, Namespace: "other"},
			},
			want: false,
		},
		{
			name: "wrong name, correct namespace",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "downstream", Namespace: localBootstrapNamespace},
			},
			want: false,
		},
		{
			name:    "empty cluster",
			cluster: &fleet.Cluster{},
			want:    false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isLocalCluster(c.cluster); got != c.want {
				t.Errorf("isLocalCluster = %v, want %v", got, c.want)
			}
		})
	}
}

func TestIsManagementCluster(t *testing.T) {
	const kubeSystem = "kube-system"

	cases := []struct {
		name string
		// localUID is the UID reported by the controller's own kube-system
		// namespace; localErr, if set, is returned instead.
		localUID  string
		localErr  error
		remoteUID string
		want      bool
		wantErr   bool
	}{
		{
			name:      "matching UIDs: same cluster",
			localUID:  "uid-management",
			remoteUID: "uid-management",
			want:      true,
		},
		{
			name:      "different UIDs: different cluster",
			localUID:  "uid-management",
			remoteUID: "uid-downstream",
			want:      false,
		},
		{
			// Defensive: never treat an empty UID on both sides as a match.
			name:      "empty UIDs are never considered a match",
			localUID:  "",
			remoteUID: "",
			want:      false,
		},
		{
			name:     "error fetching the local kube-system namespace propagates",
			localErr: errors.New("boom"),
			wantErr:  true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			nsController := fake.NewMockNonNamespacedControllerInterface[*corev1.Namespace, *corev1.NamespaceList](ctrl)
			if c.localErr != nil {
				nsController.EXPECT().Get(kubeSystem, gomock.Any()).Return(nil, c.localErr)
			} else {
				nsController.EXPECT().Get(kubeSystem, gomock.Any()).Return(&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: kubeSystem, UID: types.UID(c.localUID)},
				}, nil)
			}

			kc := kubernetesfake.NewSimpleClientset(&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: kubeSystem, UID: types.UID(c.remoteUID)},
			})

			ih := importHandler{ctx: context.Background(), namespaceController: nsController}
			got, err := ih.isManagementCluster(kc)
			if (err != nil) != c.wantErr {
				t.Fatalf("isManagementCluster error = %v, wantErr %v", err, c.wantErr)
			}
			if got != c.want {
				t.Errorf("isManagementCluster = %v, want %v", got, c.want)
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
	setLocalBootstrapConfig(t)
	cluster := &fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fleet.LocalClusterName,
			Namespace: localBootstrapNamespace,
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
	setLocalBootstrapConfig(t)

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
					Name:      fleet.LocalClusterName,
					Namespace: localBootstrapNamespace,
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
					Name:      fleet.LocalClusterName,
					Namespace: localBootstrapNamespace,
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
		"non-local cluster carrying the label is not disabled: status preserved, no teardown": {
			// The disabling label only applies to the management cluster. A
			// downstream cluster (wrong namespace) carrying it must not take the
			// teardown/clear path: with an empty ClientID importCluster returns
			// the status untouched, leaving the agent in place.
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fleet.LocalClusterName,
					Namespace: "some-downstream-ns",
					Labels: map[string]string{
						fleet.LocalAgentDisabledLabel: "true",
					},
				},
				Spec: fleet.ClusterSpec{
					KubeConfigSecret: "downstream-kubeconfig",
					ClientID:         "", // makes importCluster return early, untouched
				},
			},
			status: fleet.ClusterStatus{
				Agent:                   fleet.AgentStatus{Namespace: "cattle-fleet-system"},
				AgentDeployedGeneration: new(int64),
			},
			wantStatus: fleet.ClusterStatus{
				Agent:                   fleet.AgentStatus{Namespace: "cattle-fleet-system"},
				AgentDeployedGeneration: new(int64),
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

func TestGetPullSecrets(t *testing.T) {
	testCases := []struct {
		name               string
		config             config.Config
		cluster            fleet.Cluster
		expectedSecretRefs []corev1.LocalObjectReference
		expectedPropagate  bool
	}{
		{
			name: "cluster-level image pull secrets are not propagated",
			cluster: fleet.Cluster{
				Spec: fleet.ClusterSpec{
					AgentPullSecrets: &[]corev1.LocalObjectReference{
						{
							Name: "cluster-level",
						},
					},
				},
			},
			expectedSecretRefs: []corev1.LocalObjectReference{
				{
					Name: "cluster-level",
				},
			},
			expectedPropagate: false,
		},
		{
			name: "without cluster-level image pull secrets, config pull secrets are propagated",
			config: config.Config{
				ImagePullSecrets: []corev1.LocalObjectReference{
					{
						Name: "from-config",
					},
				},
			},
			cluster: fleet.Cluster{
				Spec: fleet.ClusterSpec{AgentPullSecrets: nil},
			},
			expectedSecretRefs: []corev1.LocalObjectReference{
				{
					Name: "from-config",
				},
			},
			expectedPropagate: true,
		},
		{
			name: "with both cluster-level and config secrets specified, cluster-level secrets are used and not propagated",
			config: config.Config{
				ImagePullSecrets: []corev1.LocalObjectReference{
					{
						Name: "from-config",
					},
				},
			},
			cluster: fleet.Cluster{
				Spec: fleet.ClusterSpec{
					AgentPullSecrets: &[]corev1.LocalObjectReference{
						{
							Name: "cluster-level",
						},
					},
				},
			},
			expectedSecretRefs: []corev1.LocalObjectReference{
				{
					Name: "cluster-level",
				},
			},
			expectedPropagate: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			refs, propagate := getPullSecrets(&tc.config, &tc.cluster)

			if len(refs) != len(tc.expectedSecretRefs) {
				t.Errorf("expected image pull secret refs %v, got %v", tc.expectedSecretRefs, refs)
			}

			for idx := range tc.expectedSecretRefs {
				if refs[idx] != tc.expectedSecretRefs[idx] {
					t.Fatalf("expected image pull secret refs at index %d to be %v, got %v", idx, tc.expectedSecretRefs[idx], refs[idx])
				}
			}

			if propagate != tc.expectedPropagate {
				t.Errorf("expected propagate to be %t, got %t", tc.expectedPropagate, propagate)
			}
		})
	}
}
