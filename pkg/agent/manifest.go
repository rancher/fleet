package agent

import (
	"path"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/pkg/basic"
	"github.com/rancher/fleet/pkg/config"
	"github.com/rancher/wrangler/pkg/name"

	corev1 "k8s.io/api/core/v1"
	networkv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	DebugLevel = 0
)

const (
	DefaultName = "fleet-agent"
)

type ManifestOptions struct {
	AgentEnvVars          []corev1.EnvVar
	AgentImage            string
	AgentImagePullPolicy  string
	CheckinInterval       string
	Generation            string
	PrivateRepoURL        string
	SystemDefaultRegistry string
}

// Manifest builds and returns a deployment manifest for the fleet-agent with a
// cluster role, two service accounts and a network policy
//
// This is called by both, import and manageagent.
func Manifest(namespace string, agentScope string, opts ManifestOptions) []runtime.Object {
	if opts.AgentImage == "" {
		opts.AgentImage = config.DefaultAgentImage
	}

	sa := basic.ServiceAccount(namespace, DefaultName)

	logrus.Debugf("Building manifest for fleet-agent in namespace %s (sa: %s)", namespace, sa.Name)

	defaultSa := basic.ServiceAccount(namespace, "default")
	defaultSa.AutomountServiceAccountToken = new(bool)

	clusterRole := []runtime.Object{
		&rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: name.SafeConcatName(sa.Namespace, sa.Name, "role"),
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{rbacv1.VerbAll},
					APIGroups: []string{rbacv1.APIGroupAll},
					Resources: []string{rbacv1.ResourceAll},
				},
			},
		},
		&rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: name.SafeConcatName(sa.Namespace, sa.Name, "role", "binding"),
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      sa.Name,
					Namespace: sa.Namespace,
				},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     name.SafeConcatName(sa.Namespace, sa.Name, "role"),
			},
		},
	}

	// PrivateRepoURL = registry.yourdomain.com:5000
	// DefaultAgentImage = "rancher/fleet-agent" + ":" + version.Version
	image := resolve(opts.SystemDefaultRegistry, opts.PrivateRepoURL, opts.AgentImage)

	// if debug is enabled in controller, enable in agent too
	debug := logrus.IsLevelEnabled(logrus.DebugLevel)
	dep := basic.Deployment(namespace, DefaultName, image, opts.AgentImagePullPolicy, DefaultName, false, debug)
	dep.Spec.Template.Spec.Containers[0].Env = append(dep.Spec.Template.Spec.Containers[0].Env,
		corev1.EnvVar{
			Name:  "AGENT_SCOPE",
			Value: agentScope,
		},
		corev1.EnvVar{
			Name:  "CHECKIN_INTERVAL",
			Value: opts.CheckinInterval,
		},
		corev1.EnvVar{
			Name:  "GENERATION",
			Value: opts.Generation,
		})
	if opts.AgentEnvVars != nil {
		dep.Spec.Template.Spec.Containers[0].Env = append(dep.Spec.Template.Spec.Containers[0].Env, opts.AgentEnvVars...)
	}
	if debug {
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
		ObjectMeta: metav1.ObjectMeta{
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
			PodSelector: metav1.LabelSelector{},
		},
	}

	var objs []runtime.Object
	objs = append(objs, clusterRole...)
	objs = append(objs, sa, defaultSa, dep, networkPolicy)

	return objs
}

func resolve(global, prefix, image string) string {
	if global != "" && prefix != "" {
		image = strings.TrimPrefix(image, global)
	}
	if prefix != "" && !strings.HasPrefix(image, prefix) {
		return path.Join(prefix, image)
	}

	return image
}
