package agentmanifest

import (
	"github.com/rancher/fleet/pkg/agent"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/config"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func objects(namespace, kubeconfig, image string) []runtime.Object {
	if image == "" {
		image = config.DefaultAgentImage
	}

	objs := []runtime.Object{
		&v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
				},
			},
		},
		&v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      agent.DefaultName,
				Namespace: namespace,
				Annotations: map[string]string{
					fleet.BootstrapToken: "true",
				},
			},
			Data: map[string][]byte{
				"kubeconfig": []byte(kubeconfig),
			},
		},
	}

	objs = append(objs, agent.Manifest(namespace, image)...)
	return objs
}
