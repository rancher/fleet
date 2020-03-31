package agent

import (
	"github.com/rancher/fleet/pkg/basic"
	"github.com/rancher/fleet/pkg/config"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	DefaultName = "fleet-agent"
)

func Manifest(namespace, image string) []runtime.Object {
	if image == "" {
		image = config.DefaultAgentImage
	}

	sa := basic.ServiceAccount(namespace, DefaultName)

	clusterRole := basic.ClusterRole(sa,
		rbacv1.PolicyRule{
			Verbs:     []string{rbacv1.VerbAll},
			APIGroups: []string{rbacv1.APIGroupAll},
			Resources: []string{rbacv1.ResourceAll},
		},
	)

	objs := []runtime.Object{
		basic.Deployment(namespace, DefaultName, image, DefaultName),
		sa,
	}
	objs = append(objs, clusterRole...)

	return objs
}
