package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// TestClusterNamespace tests the clusterNamespace helper function
func TestClusterNamespace(t *testing.T) {
	tests := []struct {
		name             string
		clusterNamespace string
		clusterName      string
		expected         string
	}{
		{
			name:             "generates consistent namespace name",
			clusterNamespace: "fleet-default",
			clusterName:      "my-cluster",
			expected:         "cluster-fleet-default-my-cluster-147c8d034230",
		},
		{
			name:             "different inputs produce different names",
			clusterNamespace: "fleet-local",
			clusterName:      "other-cluster",
			expected:         "cluster-fleet-local-other-cluster-5a2f0ee39189",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := clusterNamespace(tt.clusterNamespace, tt.clusterName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestAgentDeployedHelper tests the agentDeployed helper in controller.go
func TestAgentDeployedHelper(t *testing.T) {
	tests := []struct {
		name     string
		cluster  *fleet.Cluster
		expected bool
	}{
		{
			name: "returns true when agent fully deployed",
			cluster: &fleet.Cluster{
				Spec: fleet.ClusterSpec{
					RedeployAgentGeneration: 1,
					AgentNamespace:          "cattle-fleet-system",
				},
				Status: fleet.ClusterStatus{
					AgentDeployedGeneration: intPtr(1),
					AgentMigrated:           true,
					CattleNamespaceMigrated: true,
					AgentNamespaceMigrated:  true,
					AgentConfigChanged:      false,
					Agent: fleet.AgentStatus{
						Namespace: "cattle-fleet-system",
					},
				},
			},
			expected: true,
		},
		{
			name: "returns false when AgentConfigChanged",
			cluster: &fleet.Cluster{
				Spec: fleet.ClusterSpec{
					RedeployAgentGeneration: 1,
				},
				Status: fleet.ClusterStatus{
					AgentConfigChanged:      true,
					AgentDeployedGeneration: intPtr(1),
					AgentMigrated:           true,
					CattleNamespaceMigrated: true,
					AgentNamespaceMigrated:  true,
				},
			},
			expected: false,
		},
		{
			name: "returns false when generation mismatch",
			cluster: &fleet.Cluster{
				Spec: fleet.ClusterSpec{
					RedeployAgentGeneration: 2,
				},
				Status: fleet.ClusterStatus{
					AgentConfigChanged:      false,
					AgentDeployedGeneration: intPtr(1),
					AgentMigrated:           true,
					CattleNamespaceMigrated: true,
					AgentNamespaceMigrated:  true,
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := agentDeployed(tt.cluster)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func intPtr(i int64) *int64 {
	return &i
}
