package clusterregistrationtoken

import (
	"context"
	"time"

	"github.com/rancher/fleet/pkg/config"

	yaml "sigs.k8s.io/yaml"

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
	systemNamespace             string
	systemRegistrationNamespace string
	clusterRegistrationTokens   fleetcontrollers.ClusterRegistrationTokenController
	serviceAccountCache         corecontrollers.ServiceAccountCache
	secretsCache                corecontrollers.SecretCache
}

func Register(ctx context.Context,
	systemNamespace string,
	systemRegistrationNamespace string,
	apply apply.Apply,
	clusterGroupToken fleetcontrollers.ClusterRegistrationTokenController,
	serviceAccounts corecontrollers.ServiceAccountController,
	secretsCache corecontrollers.SecretCache,
) {
	h := &handler{
		systemNamespace:             systemNamespace,
		systemRegistrationNamespace: systemRegistrationNamespace,
		clusterRegistrationTokens:   clusterGroupToken,
		serviceAccountCache:         serviceAccounts.Cache(),
		secretsCache:                secretsCache,
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

	var (
		saName  = name.SafeConcatName(token.Name, string(token.UID))
		secrets []runtime.Object
	)
	status.SecretName = ""

	sa, err := h.serviceAccountCache.Get(token.Namespace, saName)
	if apierror.IsNotFound(err) {
		// secret doesn't exist
	} else if err != nil {
		return nil, status, err
	} else if len(sa.Secrets) > 0 {
		status.SecretName = token.Name
		secrets, err = h.getValuesYAMLSecret(token, sa.Secrets[0].Name)
		if err != nil {
			return nil, status, err
		}
	}

	status.Expires = nil
	if token.Spec.TTL != nil {
		status.Expires = &metav1.Time{Time: token.CreationTimestamp.Add(token.Spec.TTL.Duration)}
	}
	return append([]runtime.Object{
		&corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      saName,
				Namespace: token.Namespace,
				Labels: map[string]string{
					fleet.ManagedLabel: "true",
				},
			},
		},
		&rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name.SafeConcatName(saName, "role"),
				Namespace: token.Namespace,
				Labels: map[string]string{
					fleet.ManagedLabel: "true",
				},
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"create"},
					APIGroups: []string{fleetgroup.GroupName},
					Resources: []string{fleet.ClusterRegistrationResourceName},
				},
			},
		},
		&rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name.SafeConcatName(saName, "to", "role"),
				Namespace: token.Namespace,
				Labels: map[string]string{
					fleet.ManagedLabel: "true",
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
	}, secrets...), status, nil
}

func (h *handler) getValuesYAMLSecret(token *fleet.ClusterRegistrationToken, secretName string) ([]runtime.Object, error) {
	if secretName == "" {
		return nil, nil
	}

	secret, err := h.secretsCache.Get(token.Namespace, secretName)
	if err != nil {
		return nil, err
	}

	values := map[string]interface{}{
		"clusterNamespace":            token.Namespace,
		"apiServerURL":                config.Get().APIServerURL,
		"apiServerCA":                 string(config.Get().APIServerCA),
		"token":                       string(secret.Data["token"]),
		"systemRegistrationNamespace": h.systemRegistrationNamespace,
	}

	if h.systemNamespace != config.DefaultNamespace {
		values["internal"] = map[string]interface{}{
			"systemNamespace": h.systemNamespace,
		}
	}

	data, err := yaml.Marshal(values)
	if err != nil {
		return nil, err
	}

	return []runtime.Object{
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      token.Name,
				Namespace: token.Namespace,
				Labels: map[string]string{
					fleet.ManagedLabel: "true",
				},
			},
			Immutable: nil,
			Data: map[string][]byte{
				"values": data,
			},
			Type: "fleet.cattle.io/cluster-registration-values",
		},
	}, nil

}

func (h *handler) deleteExpired(token *fleet.ClusterRegistrationToken) (bool, error) {
	ttl := token.Spec.TTL
	if ttl == nil || ttl.Duration <= 0 {
		return false, nil
	}
	expire := token.CreationTimestamp.Add(ttl.Duration)
	if time.Now().After(expire) {
		return true, h.clusterRegistrationTokens.Delete(token.Namespace, token.Name, nil)
	}

	h.clusterRegistrationTokens.EnqueueAfter(token.Namespace, token.Name, time.Until(time.Now()))
	return false, nil
}
