package agent

import (
	"github.com/rancher/fleet/pkg/config"
	"github.com/rancher/fleet/pkg/version"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	DefaultName = "fleet-agent"
)

func Manifest(image string) []runtime.Object {
	labels := map[string]string{
		"app":     DefaultName,
		"version": "v1",
	}

	if image == "" {
		image = "rancher/fleet-agent:" + version.Version
	}

	objs := []runtime.Object{
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      DefaultName,
				Namespace: config.Namespace,
			},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{
					MatchLabels: labels,
				},
				Template: v1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: labels,
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Name:  DefaultName,
								Image: image,
							},
						},
						ServiceAccountName: DefaultName,
					},
				},
			},
		},
		&v1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      DefaultName,
				Namespace: config.Namespace,
			},
		},
		&rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: DefaultName,
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{rbacv1.VerbAll},
					APIGroups: []string{rbacv1.APIGroupAll},
					Resources: []string{rbacv1.ResourceAll},
				},
			},
		},
		&rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: DefaultName,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      DefaultName,
					Namespace: config.Namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     DefaultName,
			},
		},
	}

	return objs
}
