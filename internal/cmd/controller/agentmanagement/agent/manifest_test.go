package agent

import (
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

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

func getAgentFromManifests(namespace string, scope string, opts ManifestOptions) *appsv1.StatefulSet {
	objects := Manifest(namespace, scope, opts)
	for _, obj := range objects {
		dep, ok := obj.(*appsv1.StatefulSet)
		if ok {
			return dep
		}
	}
	return nil
}

func TestManifestAgentTolerations(t *testing.T) {
	const namespace = "fleet-system"
	const scope = "test-scope"
	baseOpts := ManifestOptions{
		AgentEnvVars:          []corev1.EnvVar{},
		AgentImage:            "rancher/fleet:1.2.3",
		AgentImagePullPolicy:  "Always",
		AgentTolerations:      []corev1.Toleration{},
		CheckinInterval:       "1s",
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
					{Key: "fleet-agent", Operator: "Equals", Value: "true", Effect: "NoSchedule"},
				}
				return withTolerationsOpts
			},
			expectedTolerations: append(baseTolerations,
				corev1.Toleration{Key: "fleet-agent", Operator: "Equals", Value: "true", Effect: "NoSchedule"},
			),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			agent := getAgentFromManifests(namespace, scope, testCase.getOpts())
			if agent == nil {
				t.Fatal("there were no deployments returned from the manifests")
			}

			if !cmp.Equal(agent.Spec.Template.Spec.Tolerations, testCase.expectedTolerations, cmpOpt) {
				t.Fatalf("tolerations were not as expected: %v", agent.Spec.Template.Spec.Tolerations)
			}
		})
	}
}

func TestManifestAgentHostNetwork(t *testing.T) {
	const namespace = "fleet-system"
	const scope = "test-scope"
	baseOpts := ManifestOptions{
		AgentEnvVars:          []corev1.EnvVar{},
		AgentImage:            "rancher/fleet:1.2.3",
		AgentImagePullPolicy:  "Always",
		AgentTolerations:      []corev1.Toleration{},
		CheckinInterval:       "1s",
		PrivateRepoURL:        "private.rancher.com:5000",
		SystemDefaultRegistry: "default.rancher.com",
	}

	for _, testCase := range []struct {
		name            string
		getOpts         func() ManifestOptions
		expectedNetwork bool
	}{
		{
			name: "DefaultSetting",
			getOpts: func() ManifestOptions {
				return baseOpts
			},
			expectedNetwork: false,
		},
		{
			name: "With hostNetwork",
			getOpts: func() ManifestOptions {
				withHostNetwork := baseOpts
				withHostNetwork.HostNetwork = true
				return withHostNetwork
			},
			expectedNetwork: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			agent := getAgentFromManifests(namespace, scope, testCase.getOpts())
			if agent == nil {
				t.Fatal("there were no deployments returned from the manifests")
			}

			if !cmp.Equal(agent.Spec.Template.Spec.HostNetwork, testCase.expectedNetwork) {
				t.Fatalf("hostNetwork is not as expected: %v", agent.Spec.Template.Spec.HostNetwork)
			}
		})
	}
}

func TestManifestAgentAffinity(t *testing.T) {
	const namespace = "fleet-system"

	// this is the value from manifest.go
	builtinAffinity := &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
		PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{{
			Weight: 1,
			Preference: corev1.NodeSelectorTerm{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{Key: "fleet.cattle.io/agent", Operator: corev1.NodeSelectorOpIn, Values: []string{"true"}},
				},
			},
		}},
	}}

	customAffinity := &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
		PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{{
			Weight: 1,
			Preference: corev1.NodeSelectorTerm{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{Key: "custom/label", Operator: corev1.NodeSelectorOpIn, Values: []string{"true"}},
				},
			},
		}},
	}}

	for _, testCase := range []struct {
		name             string
		getOpts          func() ManifestOptions
		expectedAffinity *corev1.Affinity
	}{
		{
			name:             "Builtin Affinity",
			getOpts:          func() ManifestOptions { return ManifestOptions{} },
			expectedAffinity: builtinAffinity,
		},
		{
			name:             "Remove Affinity",
			getOpts:          func() ManifestOptions { return ManifestOptions{AgentAffinity: &corev1.Affinity{}} },
			expectedAffinity: &corev1.Affinity{},
		},
		{
			name:             "Override Affinity",
			getOpts:          func() ManifestOptions { return ManifestOptions{AgentAffinity: customAffinity} },
			expectedAffinity: customAffinity,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			agent := getAgentFromManifests(namespace, "", testCase.getOpts())
			if agent == nil {
				t.Fatal("there were no deployments returned from the manifests")
			}

			if !cmp.Equal(agent.Spec.Template.Spec.Affinity, testCase.expectedAffinity) {
				t.Fatalf("affinity was not as expected: %v %v", testCase.expectedAffinity, agent.Spec.Template.Spec.Affinity)
			}
		})
	}
}

func TestManifestAgentResources(t *testing.T) {
	const namespace = "fleet-system"

	// this is the value from manifest.go
	builtinResources := corev1.ResourceRequirements{}

	customResources := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("100Mi"),
		},

		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("50m"),
			corev1.ResourceMemory: resource.MustParse("50Mi"),
		},
	}

	for _, testCase := range []struct {
		name              string
		getOpts           func() ManifestOptions
		expectedResources corev1.ResourceRequirements
	}{
		{
			name:              "Builtin Resources",
			getOpts:           func() ManifestOptions { return ManifestOptions{} },
			expectedResources: builtinResources,
		},
		{
			name:              "Remove Resources",
			getOpts:           func() ManifestOptions { return ManifestOptions{AgentResources: &corev1.ResourceRequirements{}} },
			expectedResources: corev1.ResourceRequirements{},
		},
		{
			name:              "Override Resources",
			getOpts:           func() ManifestOptions { return ManifestOptions{AgentResources: &customResources} },
			expectedResources: customResources,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			agent := getAgentFromManifests(namespace, "", testCase.getOpts())
			if agent == nil {
				t.Fatal("there were no deployments returned from the manifests")
			}

			if !reflect.DeepEqual(agent.Spec.Template.Spec.Containers[0].Resources, testCase.expectedResources) {
				t.Fatalf("resources was not as expected: %v %v", testCase.expectedResources, agent.Spec.Template.Spec.Containers[0].Resources)
			}
		})
	}
}
