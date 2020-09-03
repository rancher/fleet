package controllers

import (
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func addData(systemNamespace, systemRegistrationNamespace string, appCtx *appContext) error {
	return appCtx.Apply.
		WithSetID("fleet-bootstrap-data").
		WithDynamicLookup().
		ApplyObjects(
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
		)
}
