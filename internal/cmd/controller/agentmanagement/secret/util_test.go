package secret_test

import (
	"testing"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/secret"
	"github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
)

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
			refs, propagate := secret.GetAgentPullSecrets(&tc.config, &tc.cluster)

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
