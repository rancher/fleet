package manageagent

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rancher/wrangler/v3/pkg/generic/fake"
	"github.com/rancher/wrangler/v3/pkg/schemes"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	networkv1 "k8s.io/api/networking/v1"
	"sigs.k8s.io/yaml"
)

func TestNewAgentBundle(t *testing.T) {
	config.Set(&config.Config{AgentCheckinInterval: metav1.Duration{Duration: 0 * time.Second}})

	// ensure leader election env is set so NewLeaderElectionOptionsWithPrefix doesn't error
	os.Setenv("FLEET_AGENT_ELECTION_LEASE_DURATION", "15s")
	os.Setenv("FLEET_AGENT_ELECTION_RENEW_DEADLINE", "10s")
	os.Setenv("FLEET_AGENT_ELECTION_RETRY_PERIOD", "2s")

	h := handler{systemNamespace: "blah"}
	obj, err := h.newAgentBundle("foo", &fleet.Cluster{Spec: fleet.ClusterSpec{AgentNamespace: "bar"}})

	if obj != nil {
		t.Fatalf("expected obj returned by newAgentBundle to be nil")
	}

	expectedStr := "interval cannot be 0"
	if !strings.Contains(err.Error(), expectedStr) {
		t.Fatalf("expected error %q returned by newAgentBundle to contain %q", err, expectedStr)
	}
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
	cfg := config.DefaultConfig()
	cfg.AgentCheckinInterval = metav1.Duration{Duration: 1 * time.Second} // non-zero to prevent errors covered elsewhere.
	config.Set(cfg)

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

	obj, err := h.newAgentBundle("ns", cluster)
	if err != nil {
		t.Fatalf("unexpected error from newAgentBundle: %v", err)
	}

	b, ok := obj.(*fleet.Bundle)
	if !ok {
		t.Fatalf("expected bundle object, got %#v", obj)
	}

	if len(b.Spec.Resources) == 0 {
		t.Fatalf("bundle resources empty")
	}

	content := b.Spec.Resources[0].Content
	docs := strings.Split(content, "\n---\n")

	var found bool
	for _, d := range docs {
		var m map[string]interface{}
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
			cluster:        &fleet.Cluster{Spec: fleet.ClusterSpec{HostNetwork: ptr.To(true)}},
			status:         fleet.ClusterStatus{AgentHostNetwork: true},
			expectedStatus: fleet.ClusterStatus{AgentHostNetwork: true},
			enqueues:       0,
		},
		{
			name:           "Changed HostNetwork",
			cluster:        &fleet.Cluster{Spec: fleet.ClusterSpec{HostNetwork: ptr.To(true)}},
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

func checkRegisterAddToScheme(t *testing.T, f func(*runtime.Scheme) error) {
	t.Helper()
	err := schemes.Register(f)
	if err != nil {
		t.Fatalf("failed to add to scheme: %v", err)
	}
}
