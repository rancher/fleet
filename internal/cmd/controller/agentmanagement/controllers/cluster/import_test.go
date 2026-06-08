package cluster

import (
	"strings"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/rancher/fleet/internal/config"
)

func TestAllowedKubeConfigSecretNamespace(t *testing.T) {
	tests := map[string]struct {
		clusterNamespace string
		fieldValue       string
		wantNamespace    string
		wantErrContains  string
	}{
		"empty field returns cluster namespace": {
			clusterNamespace: "fleet-default",
			wantNamespace:    "fleet-default",
		},
		"custom workspace cluster referencing own namespace is allowed": {
			clusterNamespace: "my-workspace",
			fieldValue:       "my-workspace",
			wantNamespace:    "my-workspace",
		},
		"cattle-system cluster can reference own namespace": {
			clusterNamespace: "cattle-system",
			fieldValue:       "cattle-system",
			wantNamespace:    "cattle-system",
		},
		"fleet-default is allowed": {
			clusterNamespace: "fleet-local",
			fieldValue:       "fleet-default",
			wantNamespace:    "fleet-default",
		},
		"fleet-local is allowed": {
			clusterNamespace: "fleet-default",
			fieldValue:       "fleet-local",
			wantNamespace:    "fleet-local",
		},
		"cattle-fleet-system is allowed": {
			clusterNamespace: "fleet-default",
			fieldValue:       config.DefaultNamespace,
			wantNamespace:    config.DefaultNamespace,
		},
		"fleet-system is allowed": {
			clusterNamespace: "fleet-default",
			fieldValue:       config.LegacyDefaultNamespace,
			wantNamespace:    config.LegacyDefaultNamespace,
		},
		"cattle-fleet-clusters-system is allowed": {
			clusterNamespace: "fleet-default",
			fieldValue:       "cattle-fleet-clusters-system",
			wantNamespace:    "cattle-fleet-clusters-system",
		},
		"fleet-clusters-system is allowed": {
			clusterNamespace: "fleet-default",
			fieldValue:       "fleet-clusters-system",
			wantNamespace:    "fleet-clusters-system",
		},
		"cluster prefix is allowed": {
			clusterNamespace: "fleet-default",
			fieldValue:       "cluster-fleet-default-my-cluster-abc123",
			wantNamespace:    "cluster-fleet-default-my-cluster-abc123",
		},
		"kube-system is rejected": {
			clusterNamespace: "fleet-default",
			fieldValue:       "kube-system",
			wantErrContains:  "not an allowed Fleet namespace",
		},
		"cattle-system is rejected": {
			clusterNamespace: "fleet-default",
			fieldValue:       "cattle-system",
			wantErrContains:  "not an allowed Fleet namespace",
		},
		"arbitrary namespace is rejected": {
			clusterNamespace: "fleet-default",
			fieldValue:       "tenant-a-secrets",
			wantErrContains:  "not an allowed Fleet namespace",
		},
		"default is rejected": {
			clusterNamespace: "fleet-default",
			fieldValue:       "default",
			wantErrContains:  "not an allowed Fleet namespace",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			cluster := &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: test.clusterNamespace,
				},
				Spec: fleet.ClusterSpec{
					KubeConfigSecretNamespace: test.fieldValue,
				},
			}

			got, err := allowedKubeConfigSecretNamespace(cluster)
			if test.wantErrContains != "" {
				if err == nil {
					t.Fatalf("expected error for namespace %q", test.fieldValue)
				}
				if !strings.Contains(err.Error(), test.wantErrContains) {
					t.Fatalf("expected error containing %q, got %q", test.wantErrContains, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error for namespace %q: %v", test.fieldValue, err)
			}
			if got != test.wantNamespace {
				t.Fatalf("expected namespace %q, got %q", test.wantNamespace, got)
			}
		})
	}
}

func TestImportClusterRejectsDisallowedKubeconfigNamespace(t *testing.T) {
	h := &importHandler{}
	config.Set(&config.Config{})
	cluster := &fleet.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bad-cluster",
			Namespace: "fleet-default",
			UID:       types.UID("test-uid"),
		},
		Spec: fleet.ClusterSpec{
			ClientID:                  "client-id",
			KubeConfigSecret:          "some-secret",
			KubeConfigSecretNamespace: "kube-system",
		},
	}

	_, err := h.importCluster(cluster, fleet.ClusterStatus{})
	if err == nil {
		t.Fatal("expected importCluster to reject disallowed kubeConfigSecretNamespace")
	}
	if !strings.Contains(err.Error(), "not an allowed Fleet namespace") {
		t.Fatalf("expected Fleet namespace validation error, got %v", err)
	}
}
