package clusterregistrationtoken

import (
	"context"
	"time"

	fleetgroup "github.com/rancher/fleet/pkg/apis/fleet.cattle.io"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/apply"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/name"
	"github.com/rancher/wrangler/pkg/relatedresource"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierror "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type handler struct {
	clusterRegistrationTokens fleetcontrollers.ClusterRegistrationTokenClient
	serviceAccountCache       corecontrollers.ServiceAccountCache
}

func Register(ctx context.Context,
	apply apply.Apply,
	clusterGroupToken fleetcontrollers.ClusterRegistrationTokenController,
	serviceAccounts corecontrollers.ServiceAccountController,
) {
	h := &handler{
		clusterRegistrationTokens: clusterGroupToken,
		serviceAccountCache:       serviceAccounts.Cache(),
	}

	fleetcontrollers.RegisterClusterRegistrationTokenGeneratingHandler(ctx,
		clusterGroupToken,
		apply,
		"",
		"cluster-group-token",
		h.OnChange,
		nil)

	relatedresource.Watch(ctx, "sa-to-cgt",
		relatedresource.OwnerResolver(true, fleet.SchemeGroupVersion.String(), "ClusterRegistrationToken"),
		clusterGroupToken, serviceAccounts)
}

func (h *handler) OnChange(token *fleet.ClusterRegistrationToken, status fleet.ClusterRegistrationTokenStatus) ([]runtime.Object, fleet.ClusterRegistrationTokenStatus, error) {
	if gone, err := h.deleteExpired(token); err != nil || gone {
		return nil, status, nil
	}

	status.SecretName = ""
	saName := name.SafeConcatName(token.Name, string(token.UID))

	sa, err := h.serviceAccountCache.Get(token.Namespace, saName)
	if apierror.IsNotFound(err) {
		// secret doesn't exist
	} else if err != nil {
		return nil, status, err
	} else if len(sa.Secrets) > 0 {
		status.SecretName = sa.Secrets[0].Name
	}

	expireTime := token.CreationTimestamp.Add(time.Second * time.Duration(token.Spec.TTLSeconds))
	status.Expires = metav1.Time{Time: expireTime}
	return []runtime.Object{
		&corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      saName,
				Namespace: token.Namespace,
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
				},
			},
		},
		&rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name.SafeConcatName(saName, "role"),
				Namespace: token.Namespace,
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
				},
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"create"},
					APIGroups: []string{fleetgroup.GroupName},
					Resources: []string{fleet.ClusterRegistrationResourceName},
				},
				{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"secrets"},
				},
			},
		},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name.SafeConcatName(saName, "to", "role"),
				Namespace: token.Namespace,
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
				},
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      saName,
					Namespace: token.Namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "Role",
				Name:     name.SafeConcatName(saName, "role"),
			},
		},
	}, status, nil
}

func (h *handler) deleteExpired(token *fleet.ClusterRegistrationToken) (bool, error) {
	ttl := token.Spec.TTLSeconds
	expire := token.CreationTimestamp.Add(time.Second * time.Duration(ttl))
	if time.Now().After(expire) {
		return true, h.clusterRegistrationTokens.Delete(token.Namespace, token.Name, nil)
	}

	return false, nil
}
