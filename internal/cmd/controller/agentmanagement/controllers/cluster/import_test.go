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

// setLocalAgentDisabledConfig sets the bootstrap namespace and the
// LocalAgentDisabled flag, so manageagent.LocalAgentDisabled reports the local
// cluster's agent as disabled without relying on any label.
func setLocalAgentDisabledConfig(t *testing.T, disabled bool) {
	t.Helper()
	config.Set(&config.Config{Bootstrap: config.Bootstrap{
		Namespace:          localBootstrapNamespace,
		LocalAgentDisabled: disabled,
	}})
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

func TestOnConfig_UnreferenceableSecret(t *testing.T) {
	// A cluster whose kubeconfig secret lives in another namespace and does
	// not accept references from the cluster's namespace must be skipped
	// during onConfig: no config change is recorded for it.
	config.Set(&config.Config{})
	cfg := config.Config{
		APIServerCA:  []byte("foo"),
		APIServerURL: "https://hello.world",
		AgentTLSMode: "system-store",
	}

	ctrl := gomock.NewController(t)
	secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
	secretsCache.EXPECT().Get("kube-system", "some-secret").Return(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "some-secret", Namespace: "kube-system"},
	}, nil)

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

	// clusters is nil: recording a config change would panic.
	ih := importHandler{clustersCache: clustersCache, secretsCache: secretsCache}
	if err := ih.onConfig(&cfg); err != nil {
		t.Errorf("unexpected error: expected nil, got %v", err)
	}
}

func TestOnConfig_KubeConfigReferenceAuthorization(t *testing.T) {
	t.Cleanup(func() { config.Set(config.DefaultConfig()) })

	cases := map[string]struct {
		secretNamespace string
		withSecret      bool
		wantEnqueue     bool
	}{
		"well-known namespace":         {secretNamespace: "fleet-default", withSecret: true, wantEnqueue: true},
		"system namespace":             {secretNamespace: config.DefaultNamespace, withSecret: true, wantEnqueue: true},
		"registration namespace":       {secretNamespace: fleetns.SystemRegistrationNamespace(config.DefaultNamespace), withSecret: true, wantEnqueue: true},
		"implicit own namespace":       {withSecret: true},
		"explicit own namespace":       {secretNamespace: "team-a", withSecret: true},
		"other namespace":              {secretNamespace: "team-b", withSecret: true},
		"generated cluster namespace":  {secretNamespace: "cluster-team-a-cluster", withSecret: true},
		"agent-initiated registration": {secretNamespace: "fleet-default"},
	}

	for _, enabled := range []bool{true, false} {
		direction := "disable compatibility"
		if enabled {
			direction = "enable compatibility"
		}
		t.Run(direction, func(t *testing.T) {
			for name, c := range cases {
				t.Run(name, func(t *testing.T) {
					cfg := config.Config{
						AllowUnlabeledKubeConfigSecretNamespaces: !enabled,
						AgentTLSMode:                             config.AgentTLSModeStrict,
					}
					config.Set(&cfg)
					deployedGeneration := int64(1)
					cluster := &fleet.Cluster{
						ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "team-a"},
						Spec: fleet.ClusterSpec{
							KubeConfigSecret:          "kubeconfig",
							KubeConfigSecretNamespace: c.secretNamespace,
							RedeployAgentGeneration:   1,
						},
						Status: fleet.ClusterStatus{
							APIServerCAHash:         hashStatusField(cfg.APIServerCA),
							AgentTLSMode:            cfg.AgentTLSMode,
							AgentDeployedGeneration: &deployedGeneration,
						},
					}
					if !c.withSecret {
						cluster.Spec.KubeConfigSecret = ""
					}

					ctrl := gomock.NewController(t)
					clustersCache := fake.NewMockCacheInterface[*fleet.Cluster](ctrl)
					clustersCache.EXPECT().List("", gomock.Eq(labels.Everything())).
						Return([]*fleet.Cluster{cluster}, nil)
					secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
					if c.withSecret {
						secretsCache.EXPECT().Get(kubeConfigSecretNamespace(cluster), "kubeconfig").
							Return(&corev1.Secret{Data: map[string][]byte{
								config.APIServerURLKey: []byte("https://cluster.example"),
							}}, nil)
					}
					clustersController := fake.NewMockControllerInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
					if c.wantEnqueue {
						clustersController.EXPECT().Enqueue(cluster.Namespace, cluster.Name)
					}
					// No UpdateStatus: API, TLS and GC settings are unchanged.
					ih := importHandler{
						clusters:      clustersController,
						clustersCache: clustersCache,
						secretsCache:  secretsCache,
					}
					cfg.AllowUnlabeledKubeConfigSecretNamespaces = enabled
					config.Set(&cfg)
					if err := ih.onConfig(&cfg); err != nil {
						t.Fatalf("unexpected config change error: %v", err)
					}
				})
			}
		})
	}
}

func TestCheckKubeConfigSecretReference(t *testing.T) {
	labeled := func(ns, from string) *corev1.Secret {
		s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "kubeconfig", Namespace: ns}}
		if from != "" {
			s.Labels = map[string]string{fleet.KubeConfigReferenceableFromLabel: from}
		}
		return s
	}

	cases := map[string]struct {
		clusterNamespace string
		secretNamespace  string
		secret           *corev1.Secret
		allowUnlabeled   bool
		wantErr          bool
	}{
		"empty field uses the cluster's namespace": {
			clusterNamespace: "fleet-default",
			secret:           labeled("fleet-default", ""),
		},
		"explicit own namespace": {
			clusterNamespace: "my-workspace",
			secretNamespace:  "my-workspace",
			secret:           labeled("my-workspace", ""),
		},
		"own namespace without secret": {
			clusterNamespace: "my-workspace",
			secretNamespace:  "my-workspace",
		},
		"other namespace, label names cluster namespace": {
			clusterNamespace: "team-a",
			secretNamespace:  "fleet-default",
			secret:           labeled("fleet-default", "team-a"),
		},
		"other namespace, unlabeled": {
			clusterNamespace: "team-a",
			secretNamespace:  "fleet-default",
			secret:           labeled("fleet-default", ""),
			wantErr:          true,
		},
		"other namespace, label names a different namespace": {
			clusterNamespace: "team-b",
			secretNamespace:  "fleet-default",
			secret:           labeled("fleet-default", "team-a"),
			wantErr:          true,
		},
		"other namespace, missing secret": {
			clusterNamespace: "team-a",
			secretNamespace:  "fleet-default",
			wantErr:          true,
		},
		"management kubeconfig from a workspace namespace": {
			clusterNamespace: "my-workspace",
			secretNamespace:  "fleet-local",
			secret:           labeled("fleet-local", ""),
			wantErr:          true,
		},
		"management kubeconfig from a namespace named like a generated cluster namespace": {
			clusterNamespace: "cluster-workspace",
			secretNamespace:  "fleet-local",
			secret:           labeled("fleet-local", ""),
			wantErr:          true,
		},
		"management kubeconfig from fleet-default": {
			clusterNamespace: "fleet-default",
			secretNamespace:  "fleet-local",
			secret:           labeled("fleet-local", ""),
			wantErr:          true,
		},
		"generated cluster namespace, unlabeled": {
			clusterNamespace: "fleet-default",
			secretNamespace:  "cluster-fleet-default-my-cluster-abc123",
			secret:           labeled("cluster-fleet-default-my-cluster-abc123", ""),
			wantErr:          true,
		},
		"allowUnlabeled: fleet-default from a workspace namespace": {
			clusterNamespace: "team-a",
			secretNamespace:  "fleet-default",
			secret:           labeled("fleet-default", ""),
			allowUnlabeled:   true,
		},
		"allowUnlabeled: label names cluster namespace": {
			clusterNamespace: "team-a",
			secretNamespace:  "fleet-default",
			secret:           labeled("fleet-default", "team-a"),
			allowUnlabeled:   true,
		},
		"allowUnlabeled: label names a different namespace": {
			clusterNamespace: "team-b",
			secretNamespace:  "fleet-default",
			secret:           labeled("fleet-default", "team-a"),
			allowUnlabeled:   true,
			wantErr:          true,
		},
		"allowUnlabeled: empty label": {
			clusterNamespace: "team-a",
			secretNamespace:  "fleet-default",
			secret: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name:      "kubeconfig",
				Namespace: "fleet-default",
				Labels:    map[string]string{fleet.KubeConfigReferenceableFromLabel: ""},
			}},
			allowUnlabeled: true,
		},
		"allowUnlabeled: fleet-local from a workspace namespace": {
			clusterNamespace: "team-a",
			secretNamespace:  "fleet-local",
			secret:           labeled("fleet-local", ""),
			allowUnlabeled:   true,
		},
		"allowUnlabeled: system namespaces": {
			clusterNamespace: "team-a",
			secretNamespace:  config.DefaultNamespace,
			allowUnlabeled:   true,
		},
		"allowUnlabeled: legacy system namespace": {
			clusterNamespace: "team-a",
			secretNamespace:  config.LegacyDefaultNamespace,
			allowUnlabeled:   true,
		},
		"allowUnlabeled: registration namespace": {
			clusterNamespace: "team-a",
			secretNamespace:  fleetns.SystemRegistrationNamespace(config.DefaultNamespace),
			allowUnlabeled:   true,
		},
		"allowUnlabeled: generated cluster namespace stays unaccepted": {
			clusterNamespace: "fleet-default",
			secretNamespace:  "cluster-fleet-default-my-cluster-abc123",
			secret:           labeled("cluster-fleet-default-my-cluster-abc123", ""),
			allowUnlabeled:   true,
			wantErr:          true,
		},
		"allowUnlabeled: kube-system stays unaccepted": {
			clusterNamespace: "fleet-default",
			secretNamespace:  "kube-system",
			secret:           labeled("kube-system", ""),
			allowUnlabeled:   true,
			wantErr:          true,
		},
		"allowUnlabeled: arbitrary namespace stays unaccepted": {
			clusterNamespace: "fleet-default",
			secretNamespace:  "tenant-a-secrets",
			allowUnlabeled:   true,
			wantErr:          true,
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			config.Set(&config.Config{AllowUnlabeledKubeConfigSecretNamespaces: c.allowUnlabeled})
			cluster := &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: c.clusterNamespace},
				Spec: fleet.ClusterSpec{
					KubeConfigSecret:          "kubeconfig",
					KubeConfigSecretNamespace: c.secretNamespace,
				},
			}

			err := checkKubeConfigSecretReference(cluster, c.secret)
			if !c.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, errKubeConfigSecretReference) {
				t.Fatalf("expected errKubeConfigSecretReference, got %v", err)
			}
			for _, want := range []string{c.secretNamespace + "/kubeconfig", c.clusterNamespace + "/test-cluster"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("expected error %q to name %q", err.Error(), want)
				}
			}
		})
	}
}

func TestKubeConfigSecret(t *testing.T) {
	config.Set(&config.Config{})
	notFound := apierrors.NewNotFound(corev1.Resource("secrets"), "kubeconfig")
	labeled := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      "kubeconfig",
		Namespace: "fleet-default",
		Labels:    map[string]string{fleet.KubeConfigReferenceableFromLabel: "team-a"},
	}}

	cases := map[string]struct {
		clusterNamespace string
		secret           *corev1.Secret
		getErr           error
		allowUnlabeled   bool
		wantSecret       bool
		check            func(error) bool
	}{
		"labeled secret in other namespace is returned": {
			clusterNamespace: "team-a",
			secret:           labeled,
			wantSecret:       true,
		},
		"labeled secret is not returned to another namespace": {
			clusterNamespace: "team-b",
			secret:           labeled,
			check:            func(err error) bool { return errors.Is(err, errKubeConfigSecretReference) },
		},
		"allowUnlabeled: labeled secret is not returned to another namespace": {
			clusterNamespace: "team-b",
			secret:           labeled,
			allowUnlabeled:   true,
			check:            func(err error) bool { return errors.Is(err, errKubeConfigSecretReference) },
		},
		"missing secret in other namespace is reported like an unlabeled one": {
			clusterNamespace: "team-a",
			getErr:           notFound,
			check: func(err error) bool {
				return errors.Is(err, errKubeConfigSecretReference) && !apierrors.IsNotFound(err)
			},
		},
		"missing secret in own namespace is reported as not found": {
			clusterNamespace: "fleet-default",
			getErr:           notFound,
			check:            apierrors.IsNotFound,
		},
		"allowUnlabeled: missing secret in a well-known namespace is reported as not found": {
			clusterNamespace: "team-a",
			getErr:           notFound,
			allowUnlabeled:   true,
			check:            apierrors.IsNotFound,
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			config.Set(&config.Config{AllowUnlabeledKubeConfigSecretNamespaces: c.allowUnlabeled})
			ctrl := gomock.NewController(t)
			secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
			secretsCache.EXPECT().Get("fleet-default", "kubeconfig").Return(c.secret, c.getErr)

			ih := importHandler{secretsCache: secretsCache}
			got, err := ih.kubeConfigSecret(&fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: c.clusterNamespace},
				Spec: fleet.ClusterSpec{
					KubeConfigSecret:          "kubeconfig",
					KubeConfigSecretNamespace: "fleet-default",
				},
			})
			if c.wantSecret {
				if err != nil || got != c.secret {
					t.Fatalf("expected secret, got %v, %v", got, err)
				}
				return
			}
			if got != nil || !c.check(err) {
				t.Fatalf("unexpected result: %v, %v", got, err)
			}
		})
	}
}

func TestCheckManagementClusterTarget(t *testing.T) {
	const (
		kubeSystem      = "kube-system"
		systemNamespace = "cattle-fleet-system"
	)

	cases := map[string]struct {
		clusterNamespace string
		// remoteUID is the kube-system UID seen through the Cluster's kubeconfig;
		// the management cluster's own is "uid-management".
		remoteUID string
		localErr  error
		wantErr   bool
		// skipsLookup means the namespace alone decides and no cluster
		// identity lookup happens.
		skipsLookup bool
	}{
		"bootstrap namespace": {
			clusterNamespace: localBootstrapNamespace,
			skipsLookup:      true,
		},
		"system namespace": {
			clusterNamespace: systemNamespace,
			skipsLookup:      true,
		},
		"system registration namespace": {
			clusterNamespace: fleetns.SystemRegistrationNamespace(systemNamespace),
			skipsLookup:      true,
		},
		"workspace namespace targeting the management cluster": {
			clusterNamespace: "my-workspace",
			remoteUID:        "uid-management",
			wantErr:          true,
		},
		"fleet-default targeting the management cluster": {
			clusterNamespace: "fleet-default",
			remoteUID:        "uid-management",
			wantErr:          true,
		},
		"namespace named like a generated cluster namespace targeting the management cluster": {
			clusterNamespace: "cluster-workspace",
			remoteUID:        "uid-management",
			wantErr:          true,
		},
		"legacy system namespace targeting the management cluster": {
			clusterNamespace: config.LegacyDefaultNamespace,
			remoteUID:        "uid-management",
			wantErr:          true,
		},
		"workspace namespace targeting a downstream cluster": {
			clusterNamespace: "my-workspace",
			remoteUID:        "uid-downstream",
		},
		"identity lookup failure": {
			clusterNamespace: "my-workspace",
			localErr:         errors.New("boom"),
			wantErr:          true,
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			config.Set(&config.Config{Bootstrap: config.Bootstrap{Namespace: localBootstrapNamespace}})
			ctrl := gomock.NewController(t)

			nsController := fake.NewMockNonNamespacedControllerInterface[*corev1.Namespace, *corev1.NamespaceList](ctrl)
			if !c.skipsLookup {
				nsController.EXPECT().Get(kubeSystem, gomock.Any()).Return(&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: kubeSystem, UID: "uid-management"},
				}, c.localErr)
			}

			kc := kubernetesfake.NewSimpleClientset(&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: kubeSystem, UID: types.UID(c.remoteUID)},
			})

			ih := importHandler{ctx: context.Background(), systemNamespace: systemNamespace, namespaceController: nsController}
			err := ih.checkManagementClusterTarget(&fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: c.clusterNamespace},
			}, kc)
			if (err != nil) != c.wantErr {
				t.Fatalf("checkManagementClusterTarget error = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

func TestImportCluster_DeployedAgentReference(t *testing.T) {
	// A Cluster whose agent is already deployed is not redeployed, but a
	// kubeconfig reference that is not accepted is still reported: the
	// returned error ends up in the Imported condition through the status
	// handler, while importCluster itself leaves the status unchanged.
	config.Set(&config.Config{})
	notFound := apierrors.NewNotFound(corev1.Resource("secrets"), "kubeconfig")

	cases := map[string]struct {
		clusterNamespace string
		emptyClientID    bool
		secret           *corev1.Secret
		getErr           error
		wantErr          bool
	}{
		"unlabeled secret in other namespace is reported": {
			clusterNamespace: "my-workspace",
			secret:           &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "kubeconfig", Namespace: "fleet-local"}},
			wantErr:          true,
		},
		"unlabeled secret in other namespace is reported without client ID": {
			clusterNamespace: "my-workspace",
			emptyClientID:    true,
			secret:           &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "kubeconfig", Namespace: "fleet-local"}},
			wantErr:          true,
		},
		"missing secret in other namespace is reported": {
			clusterNamespace: "my-workspace",
			getErr:           notFound,
			wantErr:          true,
		},
		"labeled secret in other namespace is accepted": {
			clusterNamespace: "my-workspace",
			secret: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name:      "kubeconfig",
				Namespace: "fleet-local",
				Labels:    map[string]string{fleet.KubeConfigReferenceableFromLabel: "my-workspace"},
			}},
		},
		"missing secret in own namespace is ignored, as before": {
			clusterNamespace: "fleet-local",
			getErr:           notFound,
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			secretsCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
			secretsCache.EXPECT().Get("fleet-local", "kubeconfig").Return(c.secret, c.getErr)

			generation := int64(0)
			status := fleet.ClusterStatus{
				AgentMigrated:           true,
				CattleNamespaceMigrated: true,
				AgentNamespaceMigrated:  true,
				AgentDeployedGeneration: &generation,
				Agent:                   fleet.AgentStatus{Namespace: "cattle-fleet-system"},
			}
			cluster := &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: c.clusterNamespace},
				Spec: fleet.ClusterSpec{
					ClientID:                  "client-id",
					KubeConfigSecret:          "kubeconfig",
					KubeConfigSecretNamespace: "fleet-local",
				},
				Status: status,
			}
			if c.emptyClientID {
				cluster.Spec.ClientID = ""
			}

			// Only secretsCache is set: any deployment attempt would panic.
			ih := importHandler{secretsCache: secretsCache}
			got, err := ih.importCluster(cluster, status)
			if c.wantErr != errors.Is(err, errKubeConfigSecretReference) {
				t.Fatalf("unexpected error: %v", err)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, status) {
				t.Errorf("status mismatch.\nwant: %#v\ngot:  %#v", status, got)
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
	// The local cluster, when disabled via config, must short-circuit out of
	// OnChange before it touches any of the controllers/caches — no ClientID
	// generation, no clusters.Update, nothing.
	//
	// We assert this by leaving every field of importHandler nil; reaching
	// any of them would panic.
	setLocalAgentDisabledConfig(t, true)
	cluster := &fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fleet.LocalClusterName,
			Namespace: localBootstrapNamespace,
			Labels: map[string]string{
				"name": "local",
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
	// The agent-disabled intent comes from config (Bootstrap.LocalAgentDisabled),
	// not from a label on the Cluster, so it works even with --disable-bootstrap.
	setLocalAgentDisabledConfig(t, true)

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
				},
				Spec: fleet.ClusterSpec{
					// non-empty would normally drive the deploy path,
					// but the disabled config must skip past it.
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
		"non-local cluster is not disabled: status preserved, no teardown": {
			// The disable behavior only applies to the management cluster. A
			// downstream cluster (wrong namespace) must not take the
			// teardown/clear path even while LocalAgentDisabled is set: with an
			// empty ClientID importCluster returns the status untouched, leaving
			// the agent in place.
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fleet.LocalClusterName,
					Namespace: "some-downstream-ns",
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

func TestEnqueueLocalClusterOnConfig(t *testing.T) {
	// A config change must enqueue the local cluster (by the configured bootstrap
	// namespace and well-known name) so importCluster re-evaluates the
	// agent-disabled state. This is what makes the feature work without the
	// bootstrap controller relabeling the Cluster.
	ctrl := gomock.NewController(t)
	clustersController := fake.NewMockControllerInterface[*fleet.Cluster, *fleet.ClusterList](ctrl)
	clustersController.EXPECT().Enqueue(localBootstrapNamespace, fleet.LocalClusterName)

	ih := importHandler{clusters: clustersController}
	if err := ih.enqueueLocalClusterOnConfig(&config.Config{
		Bootstrap: config.Bootstrap{Namespace: localBootstrapNamespace},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnqueueLocalClusterOnConfig_NilConfig(t *testing.T) {
	// A nil config must be a no-op: importHandler.clusters is left nil so any
	// access would panic.
	ih := importHandler{}
	if err := ih.enqueueLocalClusterOnConfig(nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
