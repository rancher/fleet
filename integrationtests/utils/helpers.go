package utils

import (
	"context"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	cluster.Status.Namespace = clusterNs
	err = k8sClient.Status().Update(ctx, cluster)
	return cluster, err
}
