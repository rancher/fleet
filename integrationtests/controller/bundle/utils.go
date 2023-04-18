package agent

import (
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	v1gen "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func createBundle(name, namespace string, bundleController v1gen.BundleController, targets []v1alpha1.BundleTarget, targetRestrictions []v1alpha1.BundleTarget) (*v1alpha1.Bundle, error) {
	// All Targets from the GitRepo are copied into TargetRestrictions. TargetRestrictions acts as a whitelist to prevent
	// the creation of BundleDeployments from Targets created from the TargetCustomizations in the fleet.yaml
	// we replicate this behaviour here since this is run in an integration tests that runs just the BundleController.
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

	return bundleController.Create(&bundle)
}

func createCluster(name, controllerNs string, clusterController v1gen.ClusterController, labels map[string]string, clusterNs string) (*v1alpha1.Cluster, error) {
	cluster := v1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: controllerNs,
			Labels:    labels,
		},
	}
	c, err := clusterController.Create(&cluster)
	if err != nil {
		return nil, err
	}
	// Need to set the status.Namespace as it is needed to create a BundleDeployment.
	// Namespace is set by the Cluster controller. We need to do it manually because we are running just the Bundle controller.
	c.Status.Namespace = clusterNs
	return clusterController.UpdateStatus(c)
}

func createClusterGroup(name, namespace string, clusterGroupController v1gen.ClusterGroupController, selector *metav1.LabelSelector) (*v1alpha1.ClusterGroup, error) {
	cg := v1alpha1.ClusterGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.ClusterGroupSpec{
			Selector: selector,
		},
	}
	return clusterGroupController.Create(&cg)
}
