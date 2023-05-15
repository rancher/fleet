package manageagent

//go:generate mockgen --build_flags=--mod=mod -destination=../../../internal/mocks/namespace_mock.go -package=mocks github.com/rancher/wrangler/pkg/generated/controllers/core/v1 NamespaceController

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/rancher/fleet/internal/mocks"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestOnClusterChangeAffinity(t *testing.T) {
	ctrl := gomock.NewController(t)
	namespaces := mocks.NewMockNamespaceController(ctrl)
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

func TestOnClusterChangeResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	namespaces := mocks.NewMockNamespaceController(ctrl)
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
	namespaces := mocks.NewMockNamespaceController(ctrl)
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
