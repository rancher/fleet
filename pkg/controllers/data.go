package controllers

import (
	fleetgroup "github.com/rancher/fleet/pkg/apis/fleet.cattle.io"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetns "github.com/rancher/fleet/pkg/namespace"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func addData(systemNamespace, systemRegistrationNamespace string, appCtx *appContext) error {
	return appCtx.Apply.
		WithSetID("fleet-bootstrap-data").
		WithDynamicLookup().
		WithNoDeleteGVK(fleetns.GVK()).
		ApplyObjects(
			&rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name: "fleet-bundle-deployment",
				},
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"get", "list", "watch"},
						APIGroups: []string{fleetgroup.GroupName},
						Resources: []string{fleet.BundleDeploymentResourceName},
					},
					{
						Verbs:     []string{"update"},
						APIGroups: []string{fleetgroup.GroupName},
						Resources: []string{fleet.BundleDeploymentResourceName + "/status"},
					},
				},
			},
			&rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{
					Name: "fleet-content",
				},
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"get"},
						APIGroups: []string{fleetgroup.GroupName},
						Resources: []string{fleet.ContentResourceName},
					},
				},
			},
			&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: systemNamespace,
				},
			},
			&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: systemRegistrationNamespace,
				},
			},
			&rbacv1.Role{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fleet-agent-get-cred",
					Namespace: systemRegistrationNamespace,
				},
				Rules: []rbacv1.PolicyRule{
					{
						Verbs:     []string{"get"},
						APIGroups: []string{""},
						Resources: []string{"secrets"},
					},
				},
			},
			&rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fleet-agent-get-cred",
					Namespace: systemRegistrationNamespace,
				},
				Subjects: []rbacv1.Subject{
					{
						Kind:     "Group",
						APIGroup: "rbac.authorization.k8s.io",
						Name:     "system:serviceaccounts",
					},
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "Role",
					Name:     "fleet-agent-get-cred",
				},
			},
			&rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name: "fleet-agent-get-content",
				},
				Subjects: []rbacv1.Subject{
					{
						Kind:     "Group",
						APIGroup: "rbac.authorization.k8s.io",
						Name:     "system:serviceaccounts",
					},
				},
				RoleRef: rbacv1.RoleRef{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "ClusterRole",
					Name:     "fleet-content",
				},
			},
		)
}
