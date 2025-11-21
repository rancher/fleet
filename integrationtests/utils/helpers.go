package utils

import (
	"context"
	"os"
	"strings"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func CreateBundle(ctx context.Context, k8sClient client.Client, name, namespace string, targets []v1alpha1.BundleTarget, targetRestrictions []v1alpha1.BundleTarget) (*v1alpha1.Bundle, error) {
	restrictions := []v1alpha1.BundleTargetRestriction{}
	for _, r := range targetRestrictions {
		restrictions = append(restrictions, v1alpha1.BundleTargetRestriction{
			Name:                 r.Name,
			ClusterName:          r.ClusterName,
			ClusterSelector:      r.ClusterSelector,
			ClusterGroup:         r.ClusterGroup,
			ClusterGroupSelector: r.ClusterGroupSelector,
		})
	}
	bundle := v1alpha1.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"foo": "bar"},
		},
		Spec: v1alpha1.BundleSpec{
			Targets:            targets,
			TargetRestrictions: restrictions,
		},
	}

	return &bundle, k8sClient.Create(ctx, &bundle)
}

func CreateCluster(ctx context.Context, k8sClient client.Client, name, controllerNs string, labels map[string]string, clusterNs string) (*v1alpha1.Cluster, error) {
	cluster := &v1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: controllerNs,
			Labels:    labels,
		},
	}
	err := k8sClient.Create(ctx, cluster)
	if err != nil {
		return nil, err
	}

	ns := types.NamespacedName{
		Namespace: controllerNs,
		Name:      name,
	}
	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		c := &v1alpha1.Cluster{}
		err := k8sClient.Get(ctx, ns, c)
		if err != nil {
			return err
		}
		c.Status.Namespace = clusterNs
		return k8sClient.Status().Update(ctx, c)
	})
	return cluster, err
}

// ExtractResourceLogs extracts log lines related to a specific resource name
func ExtractResourceLogs(allLogs, resourceName string) string {
	var resourceLogs []string
	for _, line := range strings.Split(allLogs, "\n") {
		if strings.Contains(line, resourceName) {
			resourceLogs = append(resourceLogs, line)
		}
	}
	return strings.Join(resourceLogs, "\n")
}

// DisableReaper disables the testcontainers reaper (Ryuk) to avoid issues
// with Docker container state in local development environments.
// The reaper is mainly useful in CI but often causes race conditions locally.
// This should be called in init() functions of test packages that use testcontainers.
func DisableReaper() {
	if os.Getenv("TESTCONTAINERS_RYUK_DISABLED") == "" {
		os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")
	}
}
