// Copyright (c) 2021-2023 SUSE LLC

package reconciler

import (
	"context"

	"github.com/rancher/fleet/internal/names"

	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (r *GitJobReconciler) createJobRBAC(ctx context.Context, gitRepo *v1alpha1.GitRepo) error {
	saName := names.SafeConcatName("git", gitRepo.Name)

	if err := r.createServiceAccount(ctx, gitRepo, saName); err != nil {
		return err
	}

	if err := r.createOrUpdateRole(ctx, gitRepo, saName); err != nil {
		return err
	}

	if err := r.createOrUpdateRoleBinding(ctx, gitRepo, saName); err != nil {
		return err
	}

	return nil
}

func (r *GitJobReconciler) createServiceAccount(ctx context.Context, gitRepo *v1alpha1.GitRepo, saName string) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: gitRepo.Namespace,
		},
	}
	if err := controllerutil.SetControllerReference(gitRepo, sa, r.Scheme); err != nil {
		return err
	}
	// No update needed, values are the same. So we ignore AlreadyExists.
	if err := r.Create(ctx, sa); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func (r *GitJobReconciler) createOrUpdateRole(ctx context.Context, gitRepo *v1alpha1.GitRepo, saName string) error {
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: gitRepo.Namespace, Name: saName}}
	if err := controllerutil.SetControllerReference(gitRepo, role, r.Scheme); err != nil {
		return err
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Rules = []rbacv1.PolicyRule{
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
			{
				Verbs:     []string{"get", "create", "update", "delete"},
				APIGroups: []string{""},
				Resources: []string{"secrets"},
			},
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (r *GitJobReconciler) createOrUpdateRoleBinding(ctx context.Context, gitRepo *v1alpha1.GitRepo, saName string) error {
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: gitRepo.Namespace,
		},
	}
	if err := controllerutil.SetControllerReference(gitRepo, rb, r.Scheme); err != nil {
		return err
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		rb.Subjects = []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      saName,
			Namespace: gitRepo.Namespace,
		}}
		rb.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     saName,
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}
