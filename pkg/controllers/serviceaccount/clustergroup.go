package serviceaccount

import (
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	fleetgroup "github.com/rancher/fleet/pkg/apis/fleet.cattle.io"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/controllers/role"
	"github.com/rancher/wrangler/pkg/name"
	v1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (h *handler) OnChangeForClusterGroup(key string, sa *v1.ServiceAccount) (*v1.ServiceAccount, error) {
	if sa == nil {
		return sa, h.apply.WithOwnerKey(key, schema.GroupVersionKind{
			Group:   v1.GroupName,
			Version: v1.SchemeGroupVersion.Version,
			Kind:    "ServiceAccount",
		}).ApplyObjects()
	}

	cgName := sa.Annotations[fleet.ClusterGroupAnnotation]
	if cgName == "" {
		return sa, nil
	}

	cg, err := h.clusterGroup.Get(sa.Namespace, cgName)
	if err != nil {
		return sa, nil
	}

	if cg.Status.Namespace == "" {
		h.serviceAccount.EnqueueAfter(sa.Namespace, sa.Name, 2*time.Second)
		return sa, nil
	}

	return sa, h.authorizeClusterGroup(sa, cg)
}

func (h *handler) authorizeClusterGroup(sa *v1.ServiceAccount, cg *fleet.ClusterGroup) error {
	return h.apply.WithOwner(sa).ApplyObjects(
		&rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name.SafeConcatName(sa.Name, "role"),
				Namespace: cg.Status.Namespace,
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
				},
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"create"},
					APIGroups: []string{fleetgroup.GroupName},
					Resources: []string{fleet.ClusterRegistrationRequestResourceName},
				},
			},
		},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name.SafeConcatName(sa.Name, "to", role.AgentCredentialRoleName),
				Namespace: cg.Status.Namespace,
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
				},
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      sa.Name,
					Namespace: sa.Namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "Role",
				Name:     role.AgentCredentialRoleName,
			},
		},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name.SafeConcatName(sa.Name, "to", "role"),
				Namespace: cg.Status.Namespace,
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
				},
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      sa.Name,
					Namespace: sa.Namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "Role",
				Name:     name.SafeConcatName(sa.Name, "role"),
			},
		},
	)
}
