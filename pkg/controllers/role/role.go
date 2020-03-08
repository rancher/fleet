package role

import (
	"context"
	"sort"

	"github.com/rancher/fleet/pkg/controllers/sharedindex"

	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	corecontrollers "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	rbaccontrollers "github.com/rancher/wrangler-api/pkg/generated/controllers/rbac/v1"
	"github.com/rancher/wrangler/pkg/relatedresource"
	"github.com/rancher/wrangler/pkg/slice"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	AgentCredentialSecretByNamespace = "agentCredentialSecretByNamespace"
	AgentCredentialRoleName          = "agent-credential-access"
	AgentCredentialSecretType        = "fleet.cattle.io/agent-credential"
)

type handler struct {
	clusterGroups fleetcontrollers.ClusterGroupCache
	secretCache   corecontrollers.SecretCache
	roles         rbaccontrollers.RoleClient
}

func Register(ctx context.Context,
	secrets corecontrollers.SecretController,
	roles rbaccontrollers.RoleController,
	clusterGroups fleetcontrollers.ClusterGroupController) {

	h := &handler{
		clusterGroups: clusterGroups.Cache(),
		secretCache:   secrets.Cache(),
		roles:         roles,
	}

	secrets.Cache().AddIndexer(AgentCredentialSecretByNamespace, indexSecretsByNamespace)
	roles.OnChange(ctx, "clustergroup-agent-cred-role", h.OnRole)
	relatedresource.Watch(ctx, "clustergroup-agent-cred-role-trigger", secretToRole, roles, secrets)
}

func (h *handler) OnRole(key string, role *rbacv1.Role) (*rbacv1.Role, error) {
	if role == nil || role.Name != AgentCredentialRoleName {
		return role, nil
	}

	cgs, err := h.clusterGroups.GetByIndex(sharedindex.ClusterGroupByNamespace, role.Namespace)
	if err != nil {
		return role, err
	}

	var names []string

	if len(cgs) > 0 {
		secrets, err := h.secretCache.GetByIndex(AgentCredentialSecretByNamespace, role.Namespace)
		if err != nil {
			return role, err
		}

		for _, secret := range secrets {
			names = append(names, secret.Name)
		}
		sort.Strings(names)
	}

	if len(role.Rules) != 1 || !slice.StringsEqual(role.Rules[0].ResourceNames, names) {
		role.Rules = []rbacv1.PolicyRule{
			{
				Verbs:         []string{"get"},
				APIGroups:     []string{corev1.GroupName},
				Resources:     []string{"secrets"},
				ResourceNames: names,
			},
		}
		return h.roles.Update(role)
	}

	return role, nil
}

func secretToRole(namespace, name string, obj runtime.Object) ([]relatedresource.Key, error) {
	if s, ok := obj.(*corev1.Secret); ok {
		if s.Type == AgentCredentialSecretType {
			return []relatedresource.Key{
				{
					Namespace: s.Namespace,
					Name:      AgentCredentialRoleName,
				},
			}, nil
		}
	}
	return nil, nil
}

func indexSecretsByNamespace(secret *corev1.Secret) ([]string, error) {
	if secret.Type == AgentCredentialSecretType {
		return []string{
			secret.Namespace,
		}, nil
	}
	return nil, nil
}
