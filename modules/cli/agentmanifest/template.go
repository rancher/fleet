package agentmanifest

import (
	"github.com/rancher/fleet/modules/agent/pkg/register"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func objects(namespace string, data map[string][]byte) []runtime.Object {
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
				Name:      register.BootstrapCredName,
				Namespace: namespace,
			},
			Data: data,
		},
	}

	return objs
}
