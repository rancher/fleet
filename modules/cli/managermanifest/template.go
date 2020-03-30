package managermanifest

import (
	"github.com/rancher/fleet/pkg/config"
	"github.com/rancher/fleet/pkg/crd"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	genericName = "fleet-manager"
)

func objects(namespace, managerImage string, cfg string) ([]runtime.Object, error) {
	objs := []runtime.Object{
		&v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		},
		&rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      genericName,
				Namespace: namespace,
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{rbacv1.VerbAll},
					APIGroups: []string{""},
					Resources: []string{"configmaps"},
				},
			},
		},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      genericName,
				Namespace: namespace,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					APIGroup:  "",
					Name:      genericName,
					Namespace: namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "Role",
				Name:     genericName,
			},
		},
		&rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: genericName,
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{rbacv1.VerbAll},
					APIGroups: []string{"fleet.cattle.io"},
					Resources: []string{rbacv1.ResourceAll},
				},
				{
					Verbs:     []string{rbacv1.VerbAll},
					APIGroups: []string{""},
					Resources: []string{"namespaces"},
				},
			},
			AggregationRule: nil,
		},
		&rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: genericName,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      genericName,
					Namespace: namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     genericName,
			},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      genericName,
			},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"app": genericName,
					},
				},
				Template: v1.PodTemplateSpec{
					Spec: v1.PodSpec{
						ServiceAccountName: genericName,
						Containers: []v1.Container{
							{
								Name:  genericName,
								Image: managerImage,
								Env: []v1.EnvVar{
									{
										Name: "NAMESPACE",
										ValueFrom: &v1.EnvVarSource{
											FieldRef: &v1.ObjectFieldSelector{
												FieldPath: "metadata.namespace",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		&v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      config.ManagerConfigName,
				Namespace: namespace,
			},
			Data: map[string]string{
				config.Key: cfg,
			},
		},
	}

	crds, err := crd.Objects()
	if err != nil {
		return nil, err
	}

	objs = append(objs, crds...)
	return objs, nil
}
