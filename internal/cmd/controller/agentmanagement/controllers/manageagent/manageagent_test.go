package manageagent

import (
	"maps"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/rancher/wrangler/v3/pkg/generic/fake"
	"github.com/rancher/wrangler/v3/pkg/schemes"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	networkv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/yaml"

	"github.com/rancher/fleet/internal/config"
)

func TestMain(m *testing.M) {
	// onClusterStatusChange reads the config (via reconcileAgentEnvVars and
	// updateClusterStatus), which panics if it has not been set.
	config.Set(config.DefaultConfig())
	os.Exit(m.Run())
}

func TestOnClusterChangeAffinity(t *testing.T) {
	ctrl := gomock.NewController(t)
	namespaces := fake.NewMockNonNamespacedControllerInterface[*corev1.Namespace, *corev1.NamespaceList](ctrl)
	h := &handler{namespaces: namespaces}

	// defaultAffinity from the manifest in manifest.go
	defaultAffinity := &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{Key: "fleet.cattle.io/agent", Operator: corev1.NodeSelectorOpIn, Values: []string{"true"}},
				},
			}},
		}},
	}
	hash, _ := hashStatusField(defaultAffinity)

	customAffinity := &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{Key: "foo", Operator: corev1.NodeSelectorOpIn, Values: []string{"bar"}},
				},
			}},
		}},
	}
	customHash, _ := hashStatusField(customAffinity)

	emptyHash, _ := hashStatusField(&corev1.Affinity{})

	for _, tt := range []struct {
		name           string
		cluster        *fleet.Cluster
		status         fleet.ClusterStatus
		expectedStatus fleet.ClusterStatus
		enqueues       int
	}{
		{
			name:           "Empty Affinity",
			cluster:        &fleet.Cluster{},
			status:         fleet.ClusterStatus{},
			expectedStatus: fleet.ClusterStatus{},
			enqueues:       0,
		},
		{
			name:           "Equal Affinity",
			cluster:        &fleet.Cluster{Spec: fleet.ClusterSpec{AgentAffinity: defaultAffinity}},
			status:         fleet.ClusterStatus{AgentAffinityHash: hash},
			expectedStatus: fleet.ClusterStatus{AgentAffinityHash: hash},
			enqueues:       0,
		},
		{
			name:           "Equal Custom Affinity",
			cluster:        &fleet.Cluster{Spec: fleet.ClusterSpec{AgentAffinity: customAffinity}},
			status:         fleet.ClusterStatus{AgentAffinityHash: customHash},
			expectedStatus: fleet.ClusterStatus{AgentAffinityHash: customHash},
			enqueues:       0,
		},
		{
			name:           "Changed Affinity",
			cluster:        &fleet.Cluster{Spec: fleet.ClusterSpec{AgentAffinity: customAffinity}},
			status:         fleet.ClusterStatus{AgentAffinityHash: hash},
			expectedStatus: fleet.ClusterStatus{AgentAffinityHash: customHash},
			enqueues:       1,
		},
		{
			name:           "Changed to Empty Affinity",
			cluster:        &fleet.Cluster{Spec: fleet.ClusterSpec{}},
			status:         fleet.ClusterStatus{AgentAffinityHash: customHash},
			expectedStatus: fleet.ClusterStatus{AgentAffinityHash: ""},
			enqueues:       1,
		},
		{
			name:           "Removed Affinity",
			cluster:        &fleet.Cluster{Spec: fleet.ClusterSpec{AgentAffinity: &corev1.Affinity{}}},
			status:         fleet.ClusterStatus{AgentAffinityHash: customHash},
			expectedStatus: fleet.ClusterStatus{AgentAffinityHash: emptyHash},
			enqueues:       1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			namespaces.EXPECT().Enqueue(gomock.Any()).Times(tt.enqueues)

			status, err := h.onClusterStatusChange(tt.cluster, tt.status)
			if err != nil {
				t.Error(err)
			}

			if status.AgentAffinityHash != tt.expectedStatus.AgentAffinityHash {
				t.Fatalf("agent affinity hash is not equal: %v vs %v", status.AgentAffinityHash, tt.expectedStatus.AgentAffinityHash)
			}
		})
	}
}

func TestNewAgentBundle_SortsAgentTolerations(t *testing.T) {
	// make sure config is set for newAgentBundle
	config.Set(config.DefaultConfig())

	checkRegisterAddToScheme(t, appsv1.AddToScheme)
	checkRegisterAddToScheme(t, networkv1.AddToScheme)

	h := &handler{systemNamespace: "fleet-system"}

	unsorted := []corev1.Toleration{
		{Key: "b", Value: "2", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
		{Key: "a", Value: "1", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
		{Key: "a", Value: "1", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
		{Key: "a", Value: "0", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoExecute},
	}

	cluster := &fleet.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "c1"}, Spec: fleet.ClusterSpec{AgentTolerations: unsorted}}

	wantUser := []corev1.Toleration{
		{Key: "a", Value: "0", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoExecute},
		{Key: "a", Value: "1", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
		{Key: "a", Value: "1", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
		{Key: "b", Value: "2", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
	}

	// ensure leader election env is set so NewLeaderElectionOptionsWithPrefix doesn't error
	t.Setenv("FLEET_AGENT_ELECTION_LEASE_DURATION", "15s")
	t.Setenv("FLEET_AGENT_ELECTION_RENEW_DEADLINE", "10s")
	t.Setenv("FLEET_AGENT_ELECTION_RETRY_PERIOD", "2s")

	objs, err := h.newAgentBundle("ns", cluster)
	if err != nil {
		t.Fatalf("unexpected error from newAgentBundle: %v", err)
	}

	if len(objs) < 1 {
		t.Fatalf("unexpected empty object set from newAgentBundle")
	}

	var foundBundle bool
	var b *fleet.Bundle
	for _, o := range objs {
		b, foundBundle = o.(*fleet.Bundle)

		if foundBundle {
			break
		}
	}

	if !foundBundle || b == nil {
		t.Fatalf("expected bundle object in returned agent bundle objs, got %#v", objs)
	}

	if len(b.Spec.Resources) == 0 {
		t.Fatalf("bundle resources empty")
	}

	content := b.Spec.Resources[0].Content
	docs := strings.Split(content, "\n---\n")

	var found bool
	for _, d := range docs {
		var m map[string]any
		if err := yaml.Unmarshal([]byte(d), &m); err != nil {
			continue
		}
		if kind, _ := m["kind"].(string); kind == "Deployment" {
			dep := &appsv1.Deployment{}
			if err := yaml.Unmarshal([]byte(d), dep); err != nil {
				t.Fatalf("failed to unmarshal deployment: %v", err)
			}

			wantFinal := []corev1.Toleration{
				{Key: "node.cloudprovider.kubernetes.io/uninitialized", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule},
				{Key: "cattle.io/os", Operator: corev1.TolerationOpEqual, Value: "linux", Effect: corev1.TaintEffectNoSchedule},
			}
			wantFinal = append(wantFinal, wantUser...)

			if !reflect.DeepEqual(dep.Spec.Template.Spec.Tolerations, wantFinal) {
				t.Fatalf("deployment tolerations mismatch:\n got: %#v\n want: %#v", dep.Spec.Template.Spec.Tolerations, wantFinal)
			}

			found = true
			break
		}
	}

	if !found {
		t.Fatalf("no Deployment found in bundle yaml")
	}
}

func TestNewAgentBundle_PropagatesAgentImagePullSecrets(t *testing.T) {
	testCases := []struct {
		name                       string
		clusterPullSecrets         *[]corev1.LocalObjectReference
		configPullSecrets          *[]corev1.LocalObjectReference
		expectedAgentBundleSecrets []corev1.Secret
	}{
		{
			name: "no pull secrets; none appear in the agent deployment",
		},
		{
			name: "cluster pull secrets; they do not appear in the agent deployment",
			clusterPullSecrets: &[]corev1.LocalObjectReference{
				{Name: "secret1"},
				{Name: "secret2"},
			},
			expectedAgentBundleSecrets: []corev1.Secret{},
		},
		{
			name: "config pull secrets; they appear in the agent deployment",
			configPullSecrets: &[]corev1.LocalObjectReference{
				{Name: "secret1"},
				{Name: "secret2"},
			},
			expectedAgentBundleSecrets: []corev1.Secret{
				corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "secret1",
						Namespace: "ns",
					},
					Type: corev1.SecretTypeDockercfg,
					Data: map[string][]byte{
						"field1":       []byte("bar1"),
						"other-field1": []byte("other-value1"),
					},
				},
				corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "secret2",
						Namespace: "ns",
					},
					Type: corev1.SecretTypeDockercfg,
					Data: map[string][]byte{
						"field2":       []byte("bar2"),
						"other-field2": []byte("other-value2"),
					},
				},
			},
		},
		// Not testing all combinations of config vs cluster-level image pull secrets; secret utils'
		// `GetAgentPullSecrets` has a test suite covering those.
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// make sure config is set for newAgentBundle
			cfg := config.DefaultConfig()
			if tc.configPullSecrets != nil {
				cfg.ImagePullSecrets = *tc.configPullSecrets
			}
			config.Set(cfg)

			checkRegisterAddToScheme(t, appsv1.AddToScheme)
			checkRegisterAddToScheme(t, networkv1.AddToScheme)

			systemNS := "fleet-system"

			ctrl := gomock.NewController(t)
			secretCache := fake.NewMockCacheInterface[*corev1.Secret](ctrl)
			for _, es := range tc.expectedAgentBundleSecrets {
				// secrets are expected to eventually make it into the agent namespace, but they must be sourced from
				// the controller (system) namespace
				secretCache.EXPECT().Get(systemNS, es.Name).DoAndReturn(
					func(namespace, name any) (runtime.Object, error) {
						res := es.DeepCopy()
						res.Namespace = systemNS

						return res, nil
					},
				)
			}

			h := &handler{
				systemNamespace: systemNS,
				secretCache:     secretCache,
			}

			cluster := &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "c1"},
				Spec:       fleet.ClusterSpec{AgentPullSecrets: tc.clusterPullSecrets},
			}

			// ensure leader election env is set so NewLeaderElectionOptionsWithPrefix doesn't error
			t.Setenv("FLEET_AGENT_ELECTION_LEASE_DURATION", "15s")
			t.Setenv("FLEET_AGENT_ELECTION_RENEW_DEADLINE", "10s")
			t.Setenv("FLEET_AGENT_ELECTION_RETRY_PERIOD", "2s")

			objs, err := h.newAgentBundle("ns", cluster)
			if err != nil {
				t.Fatalf("unexpected error from newAgentBundle: %v", err)
			}

			if len(objs) < 1 {
				t.Fatalf("unexpected empty object set from newAgentBundle")
			}

			var foundBundle bool
			var b *fleet.Bundle
			var foundSecrets []corev1.Secret
			for _, o := range objs {
				b, foundBundle = o.(*fleet.Bundle)

				if foundBundle {
					break
				}

				s, isSecret := o.(*corev1.Secret)
				if isSecret {
					foundSecrets = append(foundSecrets, *s)
				}
			}

			if !foundBundle || b == nil {
				t.Fatalf("expected bundle object in returned agent bundle objs, got %#v", objs)
			}

			if len(b.Spec.Resources) == 0 {
				t.Fatalf("bundle resources empty")
			}

			if len(foundSecrets) != len(tc.expectedAgentBundleSecrets) {
				t.Fatalf(
					"expected %d image pull secrets in returned agent bundle objs, got %#v",
					len(tc.expectedAgentBundleSecrets),
					foundSecrets,
				)
			}

			for idx, es := range tc.expectedAgentBundleSecrets {
				if es.Name != foundSecrets[idx].Name ||
					es.Type != foundSecrets[idx].Type ||
					len(es.Data) != len(foundSecrets[idx].Data) {
					t.Fatalf(
						"found secret at index %d does not match expectation, want %#v, got %#v",
						idx,
						es,
						foundSecrets[idx],
					)
				}

				// If name and type match, check (more expensive) that the data also does
				for k := range maps.Keys(es.Data) {
					_, foundKey := foundSecrets[idx].Data[k]
					if !foundKey {
						t.Fatalf("expected data field %q in secret %q not found", k, foundSecrets[idx].Name)
					}
				}

			}

			content := b.Spec.Resources[0].Content
			docs := strings.Split(content, "\n---\n")

			var found bool
			for _, d := range docs {
				var m map[string]any
				if err := yaml.Unmarshal([]byte(d), &m); err != nil {
					continue
				}
				if kind, _ := m["kind"].(string); kind == "Deployment" {
					dep := &appsv1.Deployment{}
					if err := yaml.Unmarshal([]byte(d), dep); err != nil {
						t.Fatalf("failed to unmarshal deployment: %v", err)
					}

					found = true
					break
				}
			}

			if !found {
				t.Fatalf("no Deployment found in bundle yaml")
			}
		})
	}
}

func TestOnClusterChangeResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	namespaces := fake.NewMockNonNamespacedControllerInterface[*corev1.Namespace, *corev1.NamespaceList](ctrl)
	h := &handler{namespaces: namespaces}

	customResources := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("100Mi"),
		},

		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("50m"),
			corev1.ResourceMemory: resource.MustParse("50Mi"),
		},
	}
	customHash, _ := hashStatusField(customResources)

	for _, tt := range []struct {
		name           string
		cluster        *fleet.Cluster
		status         fleet.ClusterStatus
		expectedStatus fleet.ClusterStatus
		enqueues       int
	}{
		{
			name:           "Empty Resources",
			cluster:        &fleet.Cluster{},
			status:         fleet.ClusterStatus{},
			expectedStatus: fleet.ClusterStatus{},
			enqueues:       0,
		},
		{
			name:           "Equal Resources",
			cluster:        &fleet.Cluster{Spec: fleet.ClusterSpec{AgentResources: &customResources}},
			status:         fleet.ClusterStatus{AgentResourcesHash: customHash},
			expectedStatus: fleet.ClusterStatus{AgentResourcesHash: customHash},
			enqueues:       0,
		},
		{
			name:           "Changed Resources",
			cluster:        &fleet.Cluster{Spec: fleet.ClusterSpec{AgentResources: &customResources}},
			status:         fleet.ClusterStatus{AgentResourcesHash: ""},
			expectedStatus: fleet.ClusterStatus{AgentResourcesHash: customHash},
			enqueues:       1,
		},
		{
			name:           "Removed Resources",
			cluster:        &fleet.Cluster{Spec: fleet.ClusterSpec{}},
			status:         fleet.ClusterStatus{AgentTolerationsHash: customHash},
			expectedStatus: fleet.ClusterStatus{AgentTolerationsHash: ""},
			enqueues:       1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			namespaces.EXPECT().Enqueue(gomock.Any()).Times(tt.enqueues)

			status, err := h.onClusterStatusChange(tt.cluster, tt.status)
			if err != nil {
				t.Error(err)
			}

			if status.AgentResourcesHash != tt.expectedStatus.AgentResourcesHash {
				t.Fatalf("agent resources hash is not equal: %v vs %v", status.AgentResourcesHash, tt.expectedStatus.AgentResourcesHash)
			}
		})
	}
}

func TestOnClusterChangeTolerations(t *testing.T) {
	ctrl := gomock.NewController(t)
	namespaces := fake.NewMockNonNamespacedControllerInterface[*corev1.Namespace, *corev1.NamespaceList](ctrl)
	h := &handler{namespaces: namespaces}

	// defaultTolerations from the manifest in manifest.go
	defaultTolerations := []corev1.Toleration{
		{
			Key:      "node.cloudprovider.kubernetes.io/uninitialized",
			Operator: corev1.TolerationOpEqual,
			Value:    "true",
			Effect:   corev1.TaintEffectNoSchedule,
		},
		{
			Key:      "cattle.io/os",
			Operator: corev1.TolerationOpEqual,
			Value:    "linux",
			Effect:   corev1.TaintEffectNoSchedule,
		},
	}
	hash, _ := hashStatusField(defaultTolerations)

	customTolerations := []corev1.Toleration{
		{
			Key:      "node.cloudprovider.kubernetes.io/windows",
			Operator: corev1.TolerationOpEqual,
			Value:    "false",
			Effect:   corev1.TaintEffectNoSchedule,
		},
	}
	customHash, _ := hashStatusField(customTolerations)

	for _, tt := range []struct {
		name           string
		cluster        *fleet.Cluster
		status         fleet.ClusterStatus
		expectedStatus fleet.ClusterStatus
		enqueues       int
	}{
		{
			name:           "Empty Resources",
			cluster:        &fleet.Cluster{},
			status:         fleet.ClusterStatus{},
			expectedStatus: fleet.ClusterStatus{},
			enqueues:       0,
		},
		{
			name:           "Equal Tolerations",
			cluster:        &fleet.Cluster{Spec: fleet.ClusterSpec{AgentTolerations: defaultTolerations}},
			status:         fleet.ClusterStatus{AgentTolerationsHash: hash},
			expectedStatus: fleet.ClusterStatus{AgentTolerationsHash: hash},
			enqueues:       0,
		},
		{
			name:           "Equal Custom Tolerations",
			cluster:        &fleet.Cluster{Spec: fleet.ClusterSpec{AgentTolerations: customTolerations}},
			status:         fleet.ClusterStatus{AgentTolerationsHash: customHash},
			expectedStatus: fleet.ClusterStatus{AgentTolerationsHash: customHash},
			enqueues:       0,
		},
		{
			name:           "Changed Tolerations",
			cluster:        &fleet.Cluster{Spec: fleet.ClusterSpec{AgentTolerations: customTolerations}},
			status:         fleet.ClusterStatus{AgentTolerationsHash: hash},
			expectedStatus: fleet.ClusterStatus{AgentTolerationsHash: customHash},
			enqueues:       1,
		},
		{
			name:           "Removed Tolerations, omitted",
			cluster:        &fleet.Cluster{Spec: fleet.ClusterSpec{}},
			status:         fleet.ClusterStatus{AgentTolerationsHash: customHash},
			expectedStatus: fleet.ClusterStatus{AgentTolerationsHash: ""},
			enqueues:       1,
		},
		{
			name:           "Removed Tolerations, empty list",
			cluster:        &fleet.Cluster{Spec: fleet.ClusterSpec{AgentTolerations: []corev1.Toleration{}}},
			status:         fleet.ClusterStatus{AgentTolerationsHash: customHash},
			expectedStatus: fleet.ClusterStatus{AgentTolerationsHash: ""},
			enqueues:       1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			namespaces.EXPECT().Enqueue(gomock.Any()).Times(tt.enqueues)

			status, err := h.onClusterStatusChange(tt.cluster, tt.status)
			if err != nil {
				t.Error(err)
			}

			if status.AgentTolerationsHash != tt.expectedStatus.AgentTolerationsHash {
				t.Fatalf("agent tolerations hash is not equal: %v vs %v", status.AgentTolerationsHash, tt.expectedStatus.AgentTolerationsHash)
			}
		})
	}
}

func TestOnClusterChangeHostNetwork(t *testing.T) {
	ctrl := gomock.NewController(t)
	namespaces := fake.NewMockNonNamespacedControllerInterface[*corev1.Namespace, *corev1.NamespaceList](ctrl)
	h := &handler{namespaces: namespaces}

	for _, tt := range []struct {
		name           string
		cluster        *fleet.Cluster
		status         fleet.ClusterStatus
		expectedStatus fleet.ClusterStatus
		enqueues       int
	}{
		{
			name:           "Empty",
			cluster:        &fleet.Cluster{},
			status:         fleet.ClusterStatus{},
			expectedStatus: fleet.ClusterStatus{},
			enqueues:       0,
		},
		{
			name:           "Equal HostNetwork",
			cluster:        &fleet.Cluster{Spec: fleet.ClusterSpec{HostNetwork: new(true)}},
			status:         fleet.ClusterStatus{AgentHostNetwork: true},
			expectedStatus: fleet.ClusterStatus{AgentHostNetwork: true},
			enqueues:       0,
		},
		{
			name:           "Changed HostNetwork",
			cluster:        &fleet.Cluster{Spec: fleet.ClusterSpec{HostNetwork: new(true)}},
			status:         fleet.ClusterStatus{AgentHostNetwork: false},
			expectedStatus: fleet.ClusterStatus{AgentHostNetwork: true},
			enqueues:       1,
		},
		{
			name:           "Removed Resources",
			cluster:        &fleet.Cluster{Spec: fleet.ClusterSpec{}},
			status:         fleet.ClusterStatus{AgentHostNetwork: true},
			expectedStatus: fleet.ClusterStatus{AgentHostNetwork: false},
			enqueues:       1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			namespaces.EXPECT().Enqueue(gomock.Any()).Times(tt.enqueues)

			status, err := h.onClusterStatusChange(tt.cluster, tt.status)
			if err != nil {
				t.Error(err)
			}

			if status.AgentHostNetwork != tt.expectedStatus.AgentHostNetwork {
				t.Fatalf("agent hostStatus is not equal: %v vs %v", status.AgentHostNetwork, tt.expectedStatus.AgentHostNetwork)
			}
		})
	}
}

// Table-driven tests covering all comparator fields used by sortTolerations
func TestSortTolerations(t *testing.T) {
	five := int64(5)
	ten := int64(10)

	tests := []struct {
		name string
		in   []corev1.Toleration
		want []corev1.Toleration
	}{
		{
			name: "basic ordering",
			in: []corev1.Toleration{
				{Key: "b", Value: "2", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
				{Key: "a", Value: "1", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "a", Value: "1", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "a", Value: "0", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoExecute},
			},
			want: []corev1.Toleration{
				{Key: "a", Value: "0", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoExecute},
				{Key: "a", Value: "1", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "a", Value: "1", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "b", Value: "2", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
			},
		},
		{
			name: "toleration seconds nil first",
			in: []corev1.Toleration{
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule, TolerationSeconds: &ten},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule}, // nil
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule, TolerationSeconds: &five},
			},
			want: []corev1.Toleration{
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule, TolerationSeconds: &five},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule, TolerationSeconds: &ten},
			},
		},
		{
			name: "key ordering",
			in: []corev1.Toleration{
				{Key: "z", Value: "x", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "a", Value: "x", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
			},
			want: []corev1.Toleration{
				{Key: "a", Value: "x", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "z", Value: "x", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
			},
		},
		{
			name: "value ordering",
			in: []corev1.Toleration{
				{Key: "k", Value: "z", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "k", Value: "a", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
			},
			want: []corev1.Toleration{
				{Key: "k", Value: "a", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "k", Value: "z", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
			},
		},
		{
			name: "operator ordering",
			in: []corev1.Toleration{
				{Key: "k", Value: "v", Operator: "", Effect: corev1.TaintEffectNoSchedule},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
			},
			want: []corev1.Toleration{
				{Key: "k", Value: "v", Operator: "", Effect: corev1.TaintEffectNoSchedule},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
			},
		},
		{
			name: "effect ordering",
			in: []corev1.Toleration{
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoExecute},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectPreferNoSchedule},
			},
			want: []corev1.Toleration{
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoExecute},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "k", Value: "v", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectPreferNoSchedule},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inCopy := make([]corev1.Toleration, len(tt.in))
			copy(inCopy, tt.in)
			sortTolerations(inCopy)
			if !reflect.DeepEqual(inCopy, tt.want) {
				t.Fatalf("%s: got:\n%#v\nwant:\n%#v", tt.name, inCopy, tt.want)
			}
		})
	}
}

func TestSkipCluster(t *testing.T) {
	now := metav1.Now()

	for _, tt := range []struct {
		name    string
		cluster *fleet.Cluster
		want    bool
	}{
		{
			name:    "nil cluster",
			cluster: nil,
			want:    true,
		},
		{
			name:    "active cluster without labels",
			cluster: &fleet.Cluster{},
			want:    false,
		},
		{
			name: "cluster being deleted",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
			},
			want: true,
		},
		{
			name: "cluster with management label",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{fleet.ClusterManagementLabel: "custom"},
				},
			},
			want: true,
		},
		{
			name: "cluster with empty management label",
			cluster: &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{fleet.ClusterManagementLabel: ""},
				},
			},
			want: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := SkipCluster(tt.cluster); got != tt.want {
				t.Fatalf("SkipCluster() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetAgentVersionCondition(t *testing.T) {
	for _, tt := range []struct {
		name              string
		agentVersion      string
		controllerVersion string
		expectedStatus    corev1.ConditionStatus
		expectedMessage   string
	}{
		{
			name:              "matching versions",
			agentVersion:      "v0.14.0",
			controllerVersion: "v0.14.0",
			expectedStatus:    corev1.ConditionTrue,
		},
		{
			name:              "matching versions without v prefix on one side",
			agentVersion:      "0.14.0",
			controllerVersion: "v0.14.0",
			expectedStatus:    corev1.ConditionTrue,
		},
		{
			name:              "outdated agent",
			agentVersion:      "v0.13.2",
			controllerVersion: "v0.14.0",
			expectedStatus:    corev1.ConditionFalse,
			expectedMessage:   "agent version v0.13.2 does not match controller version v0.14.0",
		},
		{
			name:              "no version reported",
			controllerVersion: "v0.14.0",
			expectedStatus:    corev1.ConditionUnknown,
			expectedMessage:   "agent has not reported a version",
		},
		{
			name:              "unstamped agent build",
			agentVersion:      "dev",
			controllerVersion: "v0.14.0",
			expectedStatus:    corev1.ConditionUnknown,
			expectedMessage:   `cannot compare agent version "dev" to controller version "v0.14.0"`,
		},
		{
			name:              "unstamped controller build",
			agentVersion:      "v0.14.0",
			controllerVersion: "dev",
			expectedStatus:    corev1.ConditionUnknown,
			expectedMessage:   `cannot compare agent version "v0.14.0" to controller version "dev"`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			status := fleet.ClusterStatus{
				Agent: fleet.AgentStatus{
					Version: tt.agentVersion,
				},
			}

			setAgentVersionCondition(&status, tt.controllerVersion)

			cond := condition.Cond(fleet.ClusterConditionAgentVersionUpToDate)
			if got := cond.GetStatus(&status); got != string(tt.expectedStatus) {
				t.Errorf("condition status = %q, want %q", got, tt.expectedStatus)
			}
			if got := cond.GetMessage(&status); got != tt.expectedMessage {
				t.Errorf("condition message = %q, want %q", got, tt.expectedMessage)
			}
		})
	}
}

func checkRegisterAddToScheme(t *testing.T, f func(*runtime.Scheme) error) {
	t.Helper()
	err := schemes.Register(f)
	if err != nil {
		t.Fatalf("failed to add to scheme: %v", err)
	}
}
