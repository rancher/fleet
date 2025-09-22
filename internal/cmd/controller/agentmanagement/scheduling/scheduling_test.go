package scheduling_test

import (
	"math"
	"testing"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/scheduling"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
)

func ptr[T any](v T) *T {
	return &v
}

func TestPodDisruptionBudget(t *testing.T) {
	tests := []struct {
		name    string
		spec    *fleet.PodDisruptionBudgetSpec
		wantMU  *intstr.IntOrString
		wantMA  *intstr.IntOrString
		wantErr bool
	}{
		{
			name:   "neither value set should default to maxUnavailable 0",
			spec:   &fleet.PodDisruptionBudgetSpec{},
			wantMU: &intstr.IntOrString{Type: intstr.Int, IntVal: 0},
		},
		{
			name:   "maxUnavailable set as int",
			spec:   &fleet.PodDisruptionBudgetSpec{MaxUnavailable: "1"},
			wantMU: ptr(intstr.FromInt(1)),
		},
		{
			name:   "maxUnavailable set as percent string",
			spec:   &fleet.PodDisruptionBudgetSpec{MaxUnavailable: "50%"},
			wantMU: ptr(intstr.FromString("50%")),
		},
		{
			name:   "minAvailable set as int",
			spec:   &fleet.PodDisruptionBudgetSpec{MinAvailable: "2"},
			wantMA: ptr(intstr.FromInt(2)),
		},
		{
			name:    "both values set should result in an error",
			spec:    &fleet.PodDisruptionBudgetSpec{MinAvailable: "1", MaxUnavailable: "1"},
			wantErr: true,
		},
		{
			name:   "having both values, MinAvailable as zero, should disable the zero value",
			spec:   &fleet.PodDisruptionBudgetSpec{MinAvailable: "0", MaxUnavailable: "1"},
			wantMU: ptr(intstr.FromInt(1)),
		},
		{
			name:   "having both values, MaxUnavailable as zero, should disable the zero value",
			spec:   &fleet.PodDisruptionBudgetSpec{MinAvailable: "1", MaxUnavailable: "0"},
			wantMA: ptr(intstr.FromInt(1)),
		},
		{
			name:   "having a percent value while MinAvailable is zero should disable the zero value",
			spec:   &fleet.PodDisruptionBudgetSpec{MinAvailable: "0", MaxUnavailable: "10%"},
			wantMU: ptr(intstr.FromString("10%")),
		},
		{
			name:   "having a percent value while MaxUnavailable is zero should disable the zero value",
			spec:   &fleet.PodDisruptionBudgetSpec{MinAvailable: "10%", MaxUnavailable: "0"},
			wantMA: ptr(intstr.FromString("10%")),
		},
		{
			name:    "two non-zero values shouldn't work",
			spec:    &fleet.PodDisruptionBudgetSpec{MinAvailable: "1", MaxUnavailable: "1"},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pdb, err := scheduling.PodDisruptionBudget("agent-ns", test.spec)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if pdb.Namespace != "agent-ns" {
				t.Fatalf("namespace = %s, want agent-ns", pdb.Namespace)
			}
			if pdb.Spec.Selector == nil || pdb.Spec.Selector.MatchLabels["app"] != "fleet-agent" {
				t.Fatalf("selector mismatch: %#v", pdb.Spec.Selector)
			}
			if pdb.Name != "fleet-agent-pod-disruption-budget" {
				t.Fatalf("name = %s, want fleet-agent-pod-disruption-budget", pdb.Name)
			}
			if test.wantMU != nil {
				if pdb.Spec.MaxUnavailable == nil || *pdb.Spec.MaxUnavailable != *test.wantMU {
					t.Fatalf("MaxUnavailable = %#v, want %#v", pdb.Spec.MaxUnavailable, test.wantMU)
				}
				if pdb.Spec.MinAvailable != nil {
					t.Fatalf("MinAvailable should be nil when MaxUnavailable set")
				}
			}
			if test.wantMA != nil {
				if pdb.Spec.MinAvailable == nil || *pdb.Spec.MinAvailable != *test.wantMA {
					t.Fatalf("MinAvailable = %#v, want %#v", pdb.Spec.MinAvailable, test.wantMA)
				}
				if pdb.Spec.MaxUnavailable != nil {
					t.Fatalf("MaxUnavailable should be nil when MinAvailable set")
				}
			}
		})
	}
}

func TestPriorityClass(t *testing.T) {
	tests := []struct {
		name     string
		spec     *fleet.PriorityClassSpec
		expected *schedulingv1.PriorityClass
	}{
		{
			name: "priority 1000 with preemption",
			spec: &fleet.PriorityClassSpec{
				Value:            1000,
				PreemptionPolicy: ptr(corev1.PreemptLowerPriority),
			},
			expected: &schedulingv1.PriorityClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "fleet-agent-priority-class",
				},
				Value:            1000,
				Description:      "Priority class for Fleet Agent",
				PreemptionPolicy: ptr(corev1.PreemptLowerPriority),
			},
		},
		{
			name: "priority 100 without preemption",
			spec: &fleet.PriorityClassSpec{
				Value:            1000,
				PreemptionPolicy: ptr(corev1.PreemptNever),
			},
			expected: &schedulingv1.PriorityClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "fleet-agent-priority-class",
				},
				Value:            1000,
				Description:      "Priority class for Fleet Agent",
				PreemptionPolicy: ptr(corev1.PreemptNever),
			},
		},
		{
			name: "max value allowed",
			spec: &fleet.PriorityClassSpec{
				Value:            math.MaxInt32,
				PreemptionPolicy: ptr(corev1.PreemptNever),
			},
			expected: &schedulingv1.PriorityClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "fleet-agent-priority-class",
				},
				Value:            math.MaxInt32,
				Description:      "Priority class for Fleet Agent",
				PreemptionPolicy: ptr(corev1.PreemptNever),
			},
		},
		{
			name: "least possible value allowed",
			spec: &fleet.PriorityClassSpec{
				Value:            math.MinInt32,
				PreemptionPolicy: ptr(corev1.PreemptNever),
			},
			expected: &schedulingv1.PriorityClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "fleet-agent-priority-class",
				},
				Value:            math.MinInt32,
				Description:      "Priority class for Fleet Agent",
				PreemptionPolicy: ptr(corev1.PreemptNever),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := scheduling.PriorityClass(test.spec)
			if result.Name != test.expected.Name {
				t.Fatalf("PriorityClass name mismatch: expected %s, got %s", test.expected.Name, result.Name)
			}
			if result.Value != test.expected.Value {
				t.Fatalf("PriorityClass value mismatch: expected %d, got %d", test.expected.Value, result.Value)
			}
			if result.Description != test.expected.Description {
				t.Fatalf("PriorityClass description mismatch: expected %s, got %s", test.expected.Description, result.Description)
			}
			if result.PreemptionPolicy == nil && test.expected.PreemptionPolicy != nil {
				t.Fatalf("PriorityClass preemption policy mismatch: expected %s, got nil", *test.expected.PreemptionPolicy)
			}
		})
	}
}
