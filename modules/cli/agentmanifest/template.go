package agentmanifest

import (
	"github.com/rancher/fleet/pkg/agent"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/config"
	"github.com/rancher/fleet/pkg/version"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func configMap(clusterLabels map[string]string) ([]runtime.Object, error) {
	cm, err := config.ToConfigMap(config.Namespace, config.AgentName, &config.Config{
		Labels: clusterLabels,
	})
	if err != nil {
		return nil, err
	}
	cm.Name = "fleet-agent"
	return []runtime.Object{
		cm,
	}, nil
}

func objects(kubeconfig, image string) []runtime.Object {
	if image == "" {
		image = "rancher/fleet-agent:" + version.Version
	}

	objs := []runtime.Object{
		&v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: config.Namespace,
				Annotations: map[string]string{
					fleet.ManagedAnnotation: "true",
				},
			},
		},
		&v1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      agent.DefaultName,
				Namespace: config.Namespace,
				Annotations: map[string]string{
					fleet.BootstrapToken: "true",
				},
			},
			Data: map[string][]byte{
				"kubeconfig": []byte(kubeconfig),
			},
		},
	}

	objs = append(objs, agent.Manifest(image)...)
	return objs
}
