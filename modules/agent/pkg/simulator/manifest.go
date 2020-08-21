package simulator

import (
	"strconv"

	"k8s.io/apimachinery/pkg/api/resource"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/fleet/pkg/basic"
	"github.com/rancher/fleet/pkg/config"
	"github.com/rancher/wrangler/pkg/yaml"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	DefaultName = "fleet-agent"
)

func Manifest(namespace, systemNamespace, image string, simulators int, tokenConfig []runtime.Object) ([]runtime.Object, error) {
	if image == "" {
		image = config.DefaultAgentSimulatorImage
	}

	data, err := yaml.Export(tokenConfig...)
	if err != nil {
		return nil, err
	}

	cm := basic.ConfigMap(namespace, DefaultName, "config", string(data))

	deployment := basic.Deployment(namespace, DefaultName, image, "", "")
	deployment.Spec.Template.Spec.Containers[0].Env = []v1.EnvVar{
		{
			Name:  "NAMESPACE",
			Value: systemNamespace,
		},
	}
	deployment.Spec.Template.Spec.Containers[0].VolumeMounts = []v1.VolumeMount{
		{
			Name:      "db",
			MountPath: "/var/lib/rancher/k3s/server/db",
			SubPath:   "db",
		},
		{
			Name:      "config",
			ReadOnly:  true,
			MountPath: "/var/lib/rancher/k3s/server/manifests/manifest.yaml",
			SubPath:   "config",
		},
	}
	deployment.Spec.Template.Spec.Containers[0].Args = []string{
		"fleetagent", "--simulators", strconv.Itoa(simulators),
	}
	deployment.Spec.Template.Spec.Volumes = []v1.Volume{
		{
			Name: "db",
			VolumeSource: v1.VolumeSource{
				PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{
					ClaimName: "simulator-db",
				},
			},
		},
		{
			Name: "config",
			VolumeSource: v1.VolumeSource{
				ConfigMap: &v1.ConfigMapVolumeSource{
					LocalObjectReference: v1.LocalObjectReference{
						Name: DefaultName,
					},
				},
			},
		},
	}

	return []runtime.Object{
		cm,
		deployment,
		&v1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name: "simulator-db",
			},
			Spec: v1.PersistentVolumeClaimSpec{
				AccessModes: []v1.PersistentVolumeAccessMode{
					v1.ReadWriteOnce,
				},
				Resources: v1.ResourceRequirements{
					Requests: map[v1.ResourceName]resource.Quantity{
						v1.ResourceStorage: *resource.NewScaledQuantity(100, resource.Mega),
					},
				},
			},
		},
	}, nil
}
