package target

import (
	"context"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetAllowPodSecurityNamespaceLabelsForBundle(t *testing.T) {
	tests := map[string]struct {
		policies []fleet.Policy
		expected *bool
	}{
		"no policy keeps pod-security labels filtered": {
			expected: nil,
		},
		"policy without the option keeps pod-security labels filtered": {
			policies: []fleet.Policy{{
				ObjectMeta:            metav1.ObjectMeta{Name: "p1", Namespace: "fleet-default"},
				RequireServiceAccount: true,
			}},
			expected: nil,
		},
		"policy allowing pod-security labels opts in": {
			policies: []fleet.Policy{{
				ObjectMeta:                      metav1.ObjectMeta{Name: "p1", Namespace: "fleet-default"},
				AllowPodSecurityNamespaceLabels: true,
			}},
			expected: new(true),
		},
		"one allowing policy among several opts in": {
			policies: []fleet.Policy{
				{
					ObjectMeta:            metav1.ObjectMeta{Name: "p1", Namespace: "fleet-default"},
					RequireServiceAccount: true,
				},
				{
					ObjectMeta:                      metav1.ObjectMeta{Name: "p2", Namespace: "fleet-default"},
					AllowPodSecurityNamespaceLabels: true,
				},
			},
			expected: new(true),
		},
		"policy in another namespace is ignored": {
			policies: []fleet.Policy{{
				ObjectMeta:                      metav1.ObjectMeta{Name: "p1", Namespace: "fleet-local"},
				AllowPodSecurityNamespaceLabels: true,
			}},
			expected: nil,
		},
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(fleet.AddToScheme(scheme))

	bundle := &fleet.Bundle{ObjectMeta: metav1.ObjectMeta{Name: "bundle", Namespace: "fleet-default"}}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			for i := range test.policies {
				builder = builder.WithObjects(&test.policies[i])
			}
			m := New(builder.Build(), nil)

			got, err := m.getAllowPodSecurityNamespaceLabelsForBundle(context.Background(), bundle)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			switch {
			case test.expected == nil && got != nil:
				t.Errorf("expected nil, got %v", *got)
			case test.expected != nil && got == nil:
				t.Errorf("expected %v, got nil", *test.expected)
			case test.expected != nil && *got != *test.expected:
				t.Errorf("expected %v, got %v", *test.expected, *got)
			}
		})
	}
}
