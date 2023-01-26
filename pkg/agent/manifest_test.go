package agent

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestImageResolve(t *testing.T) {
	tests := []struct {
		systemDefaultRegistry string
		privateRepoURL        string
		image                 string
		expected              string
	}{
		{"", "", "rancher/fleet:dev", "rancher/fleet:dev"},
		{"mirror.example/", "", "mirror.example/rancher/fleet:dev", "mirror.example/rancher/fleet:dev"},
		{"mirror.example/", "local.example", "mirror.example/rancher/fleet:dev", "local.example/rancher/fleet:dev"},
	}

	for _, d := range tests {
		image := resolve(d.systemDefaultRegistry, d.privateRepoURL, d.image)
		if image != d.expected {
			t.Errorf("expected %s, got %s", d.expected, image)
		}
	}
}

func getDeploymentFromManifests(namespace string, scope string, opts ManifestOptions) *appsv1.Deployment {
	objects := Manifest(namespace, scope, opts)
	for _, obj := range objects {
		dep, ok := obj.(*appsv1.Deployment)
		if ok {
			return dep
		}
	}
	return nil
}

func TestManifestAdditionalTolerations(t *testing.T) {
	const namespace = "fleet-system"
	const scope = "test-scope"
	baseOpts := ManifestOptions{
		AgentEnvVars:          []corev1.EnvVar{},
		AgentImage:            "rancher/fleet:1.2.3",
		AgentImagePullPolicy:  "Always",
		AgentTolerations:      []corev1.Toleration{},
		CheckinInterval:       "1s",
		Generation:            "100",
		PrivateRepoURL:        "private.rancher.com:5000",
		SystemDefaultRegistry: "default.rancher.com",
	}

	// these tolerations should exist regardless of what user sent
	baseTolerations := []corev1.Toleration{
		{Key: "cattle.io/os", Operator: "Equal", Value: "linux", Effect: "NoSchedule"},
		{Key: "node.cloudprovider.kubernetes.io/uninitialized", Operator: "Equal", Value: "true", Effect: "NoSchedule"},
	}

	less := func(a, b corev1.Toleration) bool { return a.Key < b.Key }
	cmpOpt := cmpopts.SortSlices(less)

	for _, testCase := range []struct {
		name                string
		getOpts             func() ManifestOptions
		expectedTolerations []corev1.Toleration
	}{
		{
			name: "BaseOpts",
			getOpts: func() ManifestOptions {
				return baseOpts
			},
			expectedTolerations: baseTolerations,
		},
		{
			name: "ExtraToleration",
			getOpts: func() ManifestOptions {
				withTolerationsOpts := baseOpts
				withTolerationsOpts.AgentTolerations = []corev1.Toleration{
					{
						Key:      "fleet-agent",
						Operator: "Equals",
						Value:    "true",
						Effect:   "NoSchedule",
					},
				}
				return withTolerationsOpts
			},
			expectedTolerations: append(baseTolerations,
				corev1.Toleration{Key: "fleet-agent", Operator: "Equals", Value: "true", Effect: "NoSchedule"},
			),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {

			agentDeployment := getDeploymentFromManifests(namespace, scope, testCase.getOpts())
			if agentDeployment == nil {
				t.Fatal("there were no deployments returned from the manifests")
			}

			if !cmp.Equal(agentDeployment.Spec.Template.Spec.Tolerations, testCase.expectedTolerations, cmpOpt) {
				t.Fatalf("tolerations were not as expected: %v", agentDeployment.Spec.Template.Spec.Tolerations)
			}
		})
	}
}
