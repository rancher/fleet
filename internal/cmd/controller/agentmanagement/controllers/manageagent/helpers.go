// Package manageagent implements the agent management controller.
package manageagent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
)

const (
	AgentBundleName = "fleet-agent"
)

// sortTolerations sorts tolerations by key and value for consistent hashing.
func sortTolerations(tols []corev1.Toleration) {
	sort.Slice(tols, func(i, j int) bool {
		a := tols[i]
		b := tols[j]

		// 1. Key
		if a.Key != b.Key {
			return a.Key < b.Key
		}

		// 2. Value
		if a.Value != b.Value {
			return a.Value < b.Value
		}

		// 3. Operator
		if a.Operator != b.Operator {
			return a.Operator < b.Operator
		}

		// 4. Effect
		if a.Effect != b.Effect {
			return a.Effect < b.Effect
		}

		// 5. TolerationSeconds
		aSeconds := int64(0)
		bSeconds := int64(0)
		if a.TolerationSeconds != nil {
			aSeconds = *a.TolerationSeconds
		}
		if b.TolerationSeconds != nil {
			bSeconds = *b.TolerationSeconds
		}
		if aSeconds != bSeconds {
			return aSeconds < bSeconds
		}

		return false
	})
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

// hashChanged checks if a field has changed by comparing its hash to a stored hash.
func hashChanged(field any, statusHash string) (bool, string, error) {
	isNil := func(field any) bool {
		switch field := field.(type) {
		case *fleet.AgentSchedulingCustomization:
			return field == nil
		case *corev1.Affinity:
			return field == nil
		case *corev1.ResourceRequirements:
			return field == nil
		case []corev1.Toleration:
			return len(field) == 0
		default:
			return false
		}
	}

	if isNil(field) {
		if statusHash != "" {
			return true, "", nil
		}
		return false, "", nil
	}

	hash, err := hashStatusField(field)
	if err != nil {
		return false, "", err
	}

	return statusHash != hash, hash, nil
}

// SkipCluster returns true if the cluster should be skipped by the agent management controller.
func SkipCluster(cluster *fleet.Cluster) bool {
	if cluster == nil {
		return true
	}
	if cluster.Labels == nil {
		return false
	}
	if cluster.Labels[fleet.ClusterManagementLabel] != "" {
		return true
	}
	return false
}
