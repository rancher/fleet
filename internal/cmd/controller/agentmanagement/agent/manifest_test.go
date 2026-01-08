package agent_test

import (
	"reflect"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/agent"
)

const namespace = "fleet-system"

var (
	second     = time.Second
	leaderOpts = cmd.LeaderElectionOptions{
		LeaseDuration: second,
		RenewDeadline: second,
		RetryPeriod:   second,
	}
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
		image := agent.Resolve(d.systemDefaultRegistry, d.privateRepoURL, d.image)
		if image != d.expected {
			t.Errorf("expected %s, got %s", d.expected, image)
		}
	}
}

func getAgentFromManifests(scope string, opts agent.ManifestOptions) *appsv1.Deployment {
	objects := agent.Manifest(namespace, scope, opts)
	for _, obj := range objects {
		dep, ok := obj.(*appsv1.Deployment)
		if ok {
			return dep
		}
	}
	return nil
}

func TestManifestAgentTolerations(t *testing.T) {
	const scope = "test-scope"
	baseOpts := agent.ManifestOptions{
		AgentEnvVars:          []corev1.EnvVar{},
		AgentImage:            "rancher/fleet:1.2.3",
		AgentImagePullPolicy:  "Always",
		AgentTolerations:      []corev1.Toleration{},
		CheckinInterval:       "1s",
		PrivateRepoURL:        "private.rancher.com:5000",
		SystemDefaultRegistry: "default.rancher.com",
		LeaderElectionOptions: leaderOpts,
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
		getOpts             func() agent.ManifestOptions
		expectedTolerations []corev1.Toleration
	}{
		{
			name: "BaseOpts",
			getOpts: func() agent.ManifestOptions {
				return baseOpts
			},
			expectedTolerations: baseTolerations,
		},
		{
			name: "ExtraToleration",
			getOpts: func() agent.ManifestOptions {
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
			agent := getAgentFromManifests(scope, testCase.getOpts())
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
	const scope = "test-scope"
	baseOpts := agent.ManifestOptions{
		AgentEnvVars:          []corev1.EnvVar{},
		AgentImage:            "rancher/fleet:1.2.3",
		AgentImagePullPolicy:  "Always",
		AgentTolerations:      []corev1.Toleration{},
		CheckinInterval:       "1s",
		PrivateRepoURL:        "private.rancher.com:5000",
		SystemDefaultRegistry: "default.rancher.com",
		LeaderElectionOptions: leaderOpts,
	}

	for _, testCase := range []struct {
		name             string
		getOpts          func() agent.ManifestOptions
		expectedNetwork  bool
		expectedStrategy appsv1.DeploymentStrategyType
	}{
		{
			name: "DefaultSetting",
			getOpts: func() agent.ManifestOptions {
				return baseOpts
			},
			expectedNetwork:  false,
			expectedStrategy: appsv1.DeploymentStrategyType(""),
		},
		{
			name: "With hostNetwork",
			getOpts: func() agent.ManifestOptions {
				withHostNetwork := baseOpts
				withHostNetwork.HostNetwork = true
				return withHostNetwork
			},
			expectedNetwork:  true,
			expectedStrategy: appsv1.RecreateDeploymentStrategyType,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			agent := getAgentFromManifests(scope, testCase.getOpts())
			if agent == nil {
				t.Fatal("there were no deployments returned from the manifests")
			}

			if !cmp.Equal(agent.Spec.Template.Spec.HostNetwork, testCase.expectedNetwork) {
				t.Fatalf("hostNetwork is not as expected: %v", agent.Spec.Template.Spec.HostNetwork)
			}

			if !cmp.Equal(agent.Spec.Strategy.Type, testCase.expectedStrategy) {
				t.Fatalf("strategy is not as expected: %v", agent.Spec.Strategy.Type)
			}
		})
	}
}

func TestManifestAgentAffinity(t *testing.T) {
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

	baseOpts := agent.ManifestOptions{
		LeaderElectionOptions: leaderOpts,
	}

	for _, testCase := range []struct {
		name             string
		getOpts          func() agent.ManifestOptions
		expectedAffinity *corev1.Affinity
	}{
		{
			name: "Builtin Affinity",
			getOpts: func() agent.ManifestOptions {
				return baseOpts
			},
			expectedAffinity: builtinAffinity,
		},
		{
			name: "Remove Affinity",
			getOpts: func() agent.ManifestOptions {
				opts := baseOpts
				opts.AgentAffinity = &corev1.Affinity{}
				return opts
			},
			expectedAffinity: &corev1.Affinity{},
		},
		{
			name: "Override Affinity",
			getOpts: func() agent.ManifestOptions {
				opts := baseOpts
				opts.AgentAffinity = customAffinity
				return opts
			},
			expectedAffinity: customAffinity,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			agent := getAgentFromManifests("", testCase.getOpts())
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

	baseOpts := agent.ManifestOptions{
		LeaderElectionOptions: leaderOpts,
	}

	for _, testCase := range []struct {
		name              string
		getOpts           func() agent.ManifestOptions
		expectedResources corev1.ResourceRequirements
	}{
		{
			name: "Builtin Resources",
			getOpts: func() agent.ManifestOptions {
				return baseOpts
			},
			expectedResources: builtinResources,
		},
		{
			name: "Remove Resources",
			getOpts: func() agent.ManifestOptions {
				opts := baseOpts
				opts.AgentResources = &corev1.ResourceRequirements{}
				return opts
			},
			expectedResources: corev1.ResourceRequirements{},
		},
		{
			name: "Override Resources",
			getOpts: func() agent.ManifestOptions {
				opts := baseOpts
				opts.AgentResources = &customResources
				return opts
			},
			expectedResources: customResources,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			agent := getAgentFromManifests("", testCase.getOpts())
			if agent == nil {
				t.Fatal("there were no deployments returned from the manifests")
			}

			if !reflect.DeepEqual(agent.Spec.Template.Spec.Containers[0].Resources, testCase.expectedResources) {
				t.Fatalf("resources was not as expected: %v %v", testCase.expectedResources, agent.Spec.Template.Spec.Containers[0].Resources)
			}
		})
	}
}

func TestPriorityClassName(t *testing.T) {
	tests := []struct {
		name              string
		priorityClassName string
	}{
		{
			name:              "empty priorityClassName",
			priorityClassName: "",
		},
		{
			name:              "priorityClassName specified",
			priorityClassName: "foo",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			d := getAgentFromManifests("test-scope", agent.ManifestOptions{
				PriorityClassName: test.priorityClassName,
			})
			if d.Spec.Template.Spec.PriorityClassName != test.priorityClassName {
				t.Fatalf("expected PriorityClassName to be %s, got %s", test.priorityClassName, d.Spec.Template.Spec.PriorityClassName)
			}
		})
	}
}
