// Package cluster implements the cluster import controller.
package cluster

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

const (
	// clusterForKubeconfigSecretIndexer indexes Clusters by the key of the kubeconfig secret they reference in their spec
	clusterForKubeconfigSecretIndexer = "agentmanagement.fleet.cattle.io/cluster-for-kubeconfig"
)

var (
	ImportTokenPrefix = "import-token-"

	errUnavailableAPIServerURL = errors.New("missing apiServerURL in fleet config for cluster auto registration")
)

// clusterNamespace returns the derived namespace name for a Cluster resource.
func clusterNamespace(clusterNs, clusterName string) string {
	return names.SafeConcatName("cluster",
		clusterNs,
		clusterName,
		names.KeyHash(clusterNs+"::"+clusterName))
}

// agentDeployed returns true if the agent has been successfully deployed to the cluster.
func agentDeployed(cluster *fleet.Cluster) bool {
	if cluster.Status.AgentConfigChanged {
		return false
	}

	if !cluster.Status.AgentMigrated {
		return false
	}

	if !cluster.Status.CattleNamespaceMigrated {
		return false
	}

	if cluster.Status.AgentDeployedGeneration == nil {
		return false
	}

	if !cluster.Status.AgentNamespaceMigrated {
		return false
	}

	if cluster.Spec.AgentNamespace != "" && cluster.Status.Agent.Namespace != cluster.Spec.AgentNamespace {
		return false
	}

	return true
}

// shouldMigrateFromLegacyNamespace returns true if the agent should be migrated from the legacy namespace to the new one.
func shouldMigrateFromLegacyNamespace(agentStatusNs string) bool {
	return !isLegacyAgentNamespaceSelectedByUser() && agentStatusNs == config.LegacyDefaultNamespace
}

// isLegacyAgentNamespaceSelectedByUser returns true if the user has explicitly selected the legacy agent namespace.
func isLegacyAgentNamespaceSelectedByUser() bool {
	cfg := config.Get()

	return os.Getenv("NAMESPACE") == config.LegacyDefaultNamespace ||
		cfg.Bootstrap.AgentNamespace == config.LegacyDefaultNamespace
}

// getKubeConfigSecretNS returns the namespace where the kubeconfig secret should be stored.
func getKubeConfigSecretNS(cluster *fleet.Cluster) string {
	if cluster.Spec.KubeConfigSecretNamespace == "" {
		return cluster.Namespace
	}

	return cluster.Spec.KubeConfigSecretNamespace
}

// hasGarbageCollectionIntervalChanged returns true if the garbage collection interval has changed.
func hasGarbageCollectionIntervalChanged(config *config.Config, cluster *fleet.Cluster) bool {
	return (config.GarbageCollectionInterval.Duration != 0 && cluster.Status.GarbageCollectionInterval == nil) ||
		(cluster.Status.GarbageCollectionInterval != nil &&
			config.GarbageCollectionInterval.Duration != cluster.Status.GarbageCollectionInterval.Duration)
}

// hashStatusField computes a hash of a field for change detection.
func hashStatusField(field any) (string, error) {
	hasher := sha256.New224()
	b, err := json.Marshal(field)
	if err != nil {
		return "", err
	}
	hasher.Write(b)
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}
