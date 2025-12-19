// Package clusterregistration implements manager-initiated and agent-initiated registration.
package clusterregistration

import (
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AgentCredentialSecretType     = "fleet.cattle.io/agent-credential" //nolint:gosec // not a credential
	clusterByClientID             = "clusterByClientID"
	clusterRegistrationByClientID = "clusterRegistrationByClientID"
	deleteSecretAfter             = durations.ClusterRegistrationDeleteDelay
)

// skipClusterRegistration returns true if the cluster registration should be skipped.
func skipClusterRegistration(cr *fleet.ClusterRegistration) bool {
	if cr == nil {
		return true
	}
	if cr.Labels == nil {
		return false
	}
	if cr.Labels[fleet.ClusterManagementLabel] != "" {
		return true
	}
	return false
}

// shouldDelete returns true if the old cluster registration should be deleted in favor of the new one.
func shouldDelete(creg fleet.ClusterRegistration, request fleet.ClusterRegistration) bool {
	return creg.Spec.ClientID == request.Spec.ClientID &&
		creg.Spec.ClientRandom != request.Spec.ClientRandom &&
		creg.Name != request.Name &&
		creg.CreationTimestamp.Time.Before(request.CreationTimestamp.Time)
}

// requestSA creates a ServiceAccount for the cluster registration.
func requestSA(saName string, cluster *fleet.Cluster, request *fleet.ClusterRegistration) *v1.ServiceAccount {
	return &v1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1.SchemeGroupVersion.String(),
			Kind:       "ServiceAccount",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: cluster.Status.Namespace,
			Labels: map[string]string{
				fleet.ManagedLabel: "true",
			},
			Annotations: map[string]string{
				fleet.ClusterAnnotation:                      cluster.Name,
				fleet.ClusterRegistrationAnnotation:          request.Name,
				fleet.ClusterRegistrationNamespaceAnnotation: request.Namespace,
			},
		},
	}
}
