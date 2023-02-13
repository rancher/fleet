package agent

import (
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/pkg/config"
	"github.com/rancher/wrangler/pkg/name"

	appsv1 "k8s.io/api/apps/v1"
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
	AgentTolerations      []corev1.Toleration
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

	sa := serviceAccount(namespace, DefaultName)

	logrus.Debugf("Building manifest for fleet-agent in namespace %s (sa: %s)", namespace, sa.Name)

	defaultSa := serviceAccount(namespace, "default")
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

	// if debug is enabled in controller, enable in agents too (unless otherwise specified)
	propagateDebug, _ := strconv.ParseBool(os.Getenv("FLEET_PROPAGATE_DEBUG_SETTINGS_TO_AGENTS"))
	debug := logrus.IsLevelEnabled(logrus.DebugLevel) && propagateDebug
	dep := agentDeployment(namespace, DefaultName, image, opts.AgentImagePullPolicy, DefaultName, false, debug)

	// additional tolerations
	dep.Spec.Template.Spec.Tolerations = append(dep.Spec.Template.Spec.Tolerations, opts.AgentTolerations...)

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

func agentDeployment(namespace, name, image, imagePullPolicy, serviceAccount string, linuxOnly, debug bool) *appsv1.Deployment {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": name,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: serviceAccount,
					Containers: []corev1.Container{
						{
							Name:            name,
							Image:           image,
							ImagePullPolicy: corev1.PullPolicy(imagePullPolicy),
							Env: []corev1.EnvVar{
								{
									Name: "NAMESPACE",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.namespace",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	if !debug {
		for _, container := range deployment.Spec.Template.Spec.Containers {
			container.SecurityContext = &corev1.SecurityContext{
				AllowPrivilegeEscalation: &[]bool{false}[0],
				ReadOnlyRootFilesystem:   &[]bool{true}[0],
				Privileged:               &[]bool{false}[0],
				Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			}
		}
		deployment.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
			RunAsNonRoot: &[]bool{true}[0],
			RunAsUser:    &[]int64{1000}[0],
			RunAsGroup:   &[]int64{1000}[0],
		}
	}
	if linuxOnly {
		deployment.Spec.Template.Spec.NodeSelector = map[string]string{"kubernetes.io/os": "linux"}
	}
	deployment.Spec.Template.Spec.Tolerations = append(deployment.Spec.Template.Spec.Tolerations, corev1.Toleration{
		Key:      "node.cloudprovider.kubernetes.io/uninitialized",
		Operator: corev1.TolerationOpEqual,
		Value:    "true",
		Effect:   corev1.TaintEffectNoSchedule,
	}, corev1.Toleration{
		Key:      "cattle.io/os",
		Operator: corev1.TolerationOpEqual,
		Value:    "linux",
		Effect:   corev1.TaintEffectNoSchedule,
	})

	return deployment
}

func serviceAccount(namespace, name string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}
