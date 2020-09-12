package agent

import (
	"github.com/rancher/fleet/pkg/basic"
	"github.com/rancher/fleet/pkg/config"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	DefaultName = "fleet-agent"
)

func Manifest(namespace, image, pullPolicy, generation string) []runtime.Object {
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

	dep := basic.Deployment(namespace, DefaultName, image, pullPolicy, DefaultName)
	dep.Spec.Template.Spec.Containers[0].Env = append(dep.Spec.Template.Spec.Containers[0].Env,
		corev1.EnvVar{
			Name:  "GENERATION",
			Value: generation,
		})

	objs := []runtime.Object{
		dep,
		sa,
	}
	objs = append(objs, clusterRole...)

	return objs
}
