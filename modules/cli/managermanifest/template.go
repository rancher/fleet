package managermanifest

import (
	"github.com/rancher/fleet/pkg/basic"
	"github.com/rancher/fleet/pkg/config"
	"github.com/rancher/fleet/pkg/crd"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	genericName = "fleet-manager"
)

func objects(namespace, managerImage string, cfg string, crdsOnly bool) ([]runtime.Object, error) {
	crds, err := crd.Objects()
	if err != nil {
		return nil, err
	}

	if crdsOnly {
		return crds, nil
	}

	serviceAccount := basic.ServiceAccount(namespace, genericName)

	clusterRole := basic.ClusterRole(serviceAccount,
		rbacv1.PolicyRule{
			Verbs:     []string{rbacv1.VerbAll},
			APIGroups: []string{"fleet.cattle.io"},
			Resources: []string{rbacv1.ResourceAll},
		},
		rbacv1.PolicyRule{
			Verbs:     []string{rbacv1.VerbAll},
			APIGroups: []string{""},
			Resources: []string{"namespaces", "serviceaccounts"},
		},
		rbacv1.PolicyRule{
			Verbs:     []string{rbacv1.VerbAll},
			APIGroups: []string{""},
			Resources: []string{"secrets"},
		},
		rbacv1.PolicyRule{
			Verbs:     []string{"list", "watch", "get"},
			APIGroups: []string{""},
			Resources: []string{"configmaps"},
		},
		rbacv1.PolicyRule{
			Verbs:     []string{rbacv1.VerbAll},
			APIGroups: []string{rbacv1.GroupName},
			Resources: []string{"clusterroles", "clusterrolebindings", "roles", "rolebindings"},
		},
	)

	role := basic.Role(serviceAccount, namespace,
		rbacv1.PolicyRule{
			Verbs:     []string{rbacv1.VerbAll},
			APIGroups: []string{""},
			Resources: []string{"configmaps"},
		},
	)

	objs := []runtime.Object{
		basic.Namespace(namespace),
		serviceAccount,
		basic.Deployment(namespace, genericName, managerImage, serviceAccount.Name),
		basic.ConfigMap(namespace, config.ManagerConfigName, config.Key, cfg),
	}

	objs = append(objs, crds...)
	objs = append(objs, clusterRole...)
	objs = append(objs, role...)
	return objs, nil
}
