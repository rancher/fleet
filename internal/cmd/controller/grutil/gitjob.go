// Copyright (c) 2021-2023 SUSE LLC

package grutil

import (
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func NewServiceAccount(namespace string, name string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func NewRole(namespace string, name string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"get", "create", "update", "list", "delete"},
				APIGroups: []string{"fleet.cattle.io"},
				Resources: []string{"bundles", "imagescans"},
			},
			{
				Verbs:     []string{"get"},
				APIGroups: []string{"fleet.cattle.io"},
				Resources: []string{"gitrepos"},
			},
		},
	}
}

func NewRoleBinding(namespace string, name string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      name,
			Namespace: namespace,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     name,
		},
	}
}
