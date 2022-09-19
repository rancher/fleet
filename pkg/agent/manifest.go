// Package agent provides the deployment manifest for the fleet-agent. (fleetcontroller)
package agent

import (
	"strconv"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/pkg/basic"
	"github.com/rancher/fleet/pkg/config"

	corev1 "k8s.io/api/core/v1"
	networkv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	DebugLevel = 0
)

const (
	DefaultName = "fleet-agent"
)

func Manifest(namespace, agentScope, image, pullPolicy, generation, checkInInterval string, agentEnvVars []corev1.EnvVar) []runtime.Object {
	if image == "" {
		image = config.DefaultAgentImage
	}

	sa := basic.ServiceAccount(namespace, DefaultName)

	defaultSa := basic.ServiceAccount(namespace, "default")
	defaultSa.AutomountServiceAccountToken = new(bool)

	clusterRole := basic.ClusterRole(sa,
		rbacv1.PolicyRule{
			Verbs:     []string{rbacv1.VerbAll},
			APIGroups: []string{rbacv1.APIGroupAll},
			Resources: []string{rbacv1.ResourceAll},
		},
	)

	dep := basic.Deployment(namespace, DefaultName, image, pullPolicy, DefaultName, false)
	dep.Spec.Template.Spec.Containers[0].Env = append(dep.Spec.Template.Spec.Containers[0].Env,
		corev1.EnvVar{
			Name:  "AGENT_SCOPE",
			Value: agentScope,
		},
		corev1.EnvVar{
			Name:  "CHECKIN_INTERVAL",
			Value: checkInInterval,
		},
		corev1.EnvVar{
			Name:  "GENERATION",
			Value: generation,
		})
	if agentEnvVars != nil {
		dep.Spec.Template.Spec.Containers[0].Env = append(dep.Spec.Template.Spec.Containers[0].Env, agentEnvVars...)
	}
	// if debug level logging is enabled in controller, enable in agent too
	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		dep.Spec.Template.Spec.Containers[0].Command = []string{
			"fleetagent",
			"--debug",
			"--debug-level",
			strconv.Itoa(DebugLevel),
		}
	}
	dep.Spec.Template.Spec.Affinity = &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{
				{
					Weight: 1,
					Preference: corev1.NodeSelectorTerm{
						MatchExpressions: []corev1.NodeSelectorRequirement{
							{
								Key:      "fleet.cattle.io/agent",
								Operator: corev1.NodeSelectorOpIn,
								Values:   []string{"true"},
							},
						},
					},
				},
			},
		},
	}

	networkPolicy := &networkv1.NetworkPolicy{
		ObjectMeta: v1.ObjectMeta{
			Name:      "default-allow-all",
			Namespace: namespace,
		},
		Spec: networkv1.NetworkPolicySpec{
			PolicyTypes: []networkv1.PolicyType{
				networkv1.PolicyTypeIngress,
				networkv1.PolicyTypeEgress,
			},
			Ingress: []networkv1.NetworkPolicyIngressRule{
				{},
			},
			Egress: []networkv1.NetworkPolicyEgressRule{
				{},
			},
			PodSelector: v1.LabelSelector{},
		},
	}

	var objs []runtime.Object
	objs = append(objs, clusterRole...)
	objs = append(objs, sa, defaultSa, dep, networkPolicy)

	return objs
}
