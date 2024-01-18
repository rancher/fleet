// Copyright (c) 2021-2023 SUSE LLC

package gitrepo

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	gitjob "github.com/rancher/fleet/pkg/apis/gitjob.cattle.io/v1"

	"github.com/rancher/wrangler/v2/pkg/yaml"

	"github.com/sirupsen/logrus"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const fleetHomeDir = "/fleet-home"

var two = int32(2)

func NewServiceAccount(namespace string, name string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func NewRole(namespace string, name string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				Verbs:     []string{"get", "create", "update", "list", "delete"},
				APIGroups: []string{"fleet.cattle.io"},
				Resources: []string{"bundles", "imagescans"},
			},
			{
				Verbs:     []string{"get"},
				APIGroups: []string{"fleet.cattle.io"},
				Resources: []string{"gitrepos"},
			},
		},
	}
}

func NewRoleBinding(namespace string, name string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      name,
			Namespace: namespace,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     name,
		},
	}
}

func MutateGitJob(gitjob *gitjob.GitJob) controllerutil.MutateFn {
	updated := gitjob.DeepCopy()
	return func() error {
		gitjob.Labels = updated.ObjectMeta.Labels
		gitjob.Annotations = updated.ObjectMeta.Annotations
		gitjob.Spec = updated.Spec
		return nil
	}
}

func NewGitJob(ctx context.Context, c client.Client, gitrepo *fleet.GitRepo, saName string, targetsConfigName string) *gitjob.GitJob {
	branch, rev := gitrepo.Spec.Branch, gitrepo.Spec.Revision
	if branch == "" && rev == "" {
		branch = "master"
	}

	syncSeconds := 0
	if gitrepo.Spec.PollingInterval != nil {
		syncSeconds = int(gitrepo.Spec.PollingInterval.Duration / time.Second)
	}

	paths := gitrepo.Spec.Paths
	if len(paths) == 0 {
		paths = []string{"."}
	}

	volumes, volumeMounts := volumes(targetsConfigName)

	if gitrepo.Spec.HelmSecretNameForPaths != "" {
		vols, volMnts := volumesFromSecret(ctx, c,
			gitrepo.Namespace,
			gitrepo.Spec.HelmSecretNameForPaths,
			"helm-secret-by-path",
		)

		volumes = append(volumes, vols...)
		volumeMounts = append(volumeMounts, volMnts...)

	} else if gitrepo.Spec.HelmSecretName != "" {
		vols, volMnts := volumesFromSecret(ctx, c,
			gitrepo.Namespace,
			gitrepo.Spec.HelmSecretName,
			"helm-secret",
		)

		volumes = append(volumes, vols...)
		volumeMounts = append(volumeMounts, volMnts...)
	}

	args, envs := argsAndEnvs(gitrepo)

	return &gitjob.GitJob{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      yaml.CleanAnnotationsForExport(gitrepo.Labels),
			Annotations: yaml.CleanAnnotationsForExport(gitrepo.Annotations),
			Name:        gitrepo.Name,
			Namespace:   gitrepo.Namespace,
		},
		Spec: gitjob.GitJobSpec{
			SyncInterval:          syncSeconds,
			ForceUpdateGeneration: gitrepo.Spec.ForceSyncGeneration,
			Git: gitjob.GitInfo{
				Credential: gitjob.Credential{
					ClientSecretName:      gitrepo.Spec.ClientSecretName,
					CABundle:              gitrepo.Spec.CABundle,
					InsecureSkipTLSverify: gitrepo.Spec.InsecureSkipTLSverify,
				},
				Provider: "polling",
				Repo:     gitrepo.Spec.Repo,
				Revision: rev,
				Branch:   branch,
			},
			JobSpec: batchv1.JobSpec{
				BackoffLimit: &two,
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						CreationTimestamp: metav1.Time{Time: time.Unix(0, 0)},
					},
					Spec: corev1.PodSpec{
						Volumes: volumes,
						SecurityContext: &corev1.PodSecurityContext{
							RunAsUser: &[]int64{1000}[0],
						},
						ServiceAccountName: saName,
						RestartPolicy:      corev1.RestartPolicyNever,
						Containers: []corev1.Container{
							{
								Name:            "fleet",
								Image:           config.Get().AgentImage,
								ImagePullPolicy: corev1.PullPolicy(config.Get().AgentImagePullPolicy),
								Command:         []string{"log.sh"},
								Args:            append(args, paths...),
								WorkingDir:      "/workspace/source",
								VolumeMounts:    volumeMounts,
								Env:             envs,
								SecurityContext: &corev1.SecurityContext{
									AllowPrivilegeEscalation: &[]bool{false}[0],
									ReadOnlyRootFilesystem:   &[]bool{true}[0],
									Privileged:               &[]bool{false}[0],
									RunAsNonRoot:             &[]bool{true}[0],
									SeccompProfile: &corev1.SeccompProfile{
										Type: corev1.SeccompProfileTypeRuntimeDefault,
									},
									Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
								},
							},
						},
						NodeSelector: map[string]string{"kubernetes.io/os": "linux"},
						Tolerations: []corev1.Toleration{{
							Key:      "cattle.io/os",
							Operator: "Equal",
							Value:    "linux",
							Effect:   "NoSchedule",
						}},
					},
				},
			},
		},
	}
}

// volumes builds sets of volumes and their volume mounts for default folders and the targets config map.
func volumes(targetsConfigName string) ([]corev1.Volume, []corev1.VolumeMount) {
	const (
		emptyDirTmpVolumeName  = "fleet-tmp-empty-dir"
		emptyDirHomeVolumeName = "fleet-home-empty-dir"
		configVolumeName       = "config"
	)

	volumes := []corev1.Volume{
		{
			Name: configVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: targetsConfigName,
					},
				},
			},
		},
		{
			Name: emptyDirTmpVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		{
			Name: emptyDirHomeVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}

	volumeMounts := []corev1.VolumeMount{
		{
			Name:      configVolumeName,
			MountPath: "/run/config",
		},
		{
			Name:      emptyDirTmpVolumeName,
			MountPath: "/tmp",
		},
		{
			Name:      emptyDirHomeVolumeName,
			MountPath: fleetHomeDir,
		},
	}

	return volumes, volumeMounts
}

// volumesFromSecret generates volumes and volume mounts from a Helm secret, assuming that that secret exists.
// If the secret has a cacerts key, it will be mounted into /etc/ssl/certs, too.
func volumesFromSecret(
	ctx context.Context,
	c client.Client,
	namespace string,
	secretName, volumeName string,
) ([]corev1.Volume, []corev1.VolumeMount) {
	volumes := []corev1.Volume{
		{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: secretName,
				},
			},
		},
	}
	volumeMounts := []corev1.VolumeMount{
		{
			Name:      volumeName,
			MountPath: "/etc/fleet/helm",
		},
	}

	// Mount a CA certificate, if specified in the secret. This is necessary to support Helm registries with
	// self-signed certificates.
	secret := &corev1.Secret{}
	_ = c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: secretName}, secret)
	if _, ok := secret.Data["cacerts"]; ok {
		certVolumeName := fmt.Sprintf("%s-cert", volumeName)

		volumes = append(volumes, corev1.Volume{
			Name: certVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: secretName,
					Items: []corev1.KeyToPath{
						{
							Key:  "cacerts",
							Path: "cacert.crt",
						},
					},
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      certVolumeName,
			MountPath: "/etc/ssl/certs",
		})
	}

	return volumes, volumeMounts
}

func argsAndEnvs(gitrepo *fleet.GitRepo) ([]string, []corev1.EnvVar) {
	args := []string{
		"fleet",
		"apply",
	}

	if logrus.IsLevelEnabled(logrus.DebugLevel) {
		args = append(args, "--debug", "--debug-level", "9")
	}

	bundleLabels := labels.Merge(gitrepo.Labels, map[string]string{
		fleet.RepoLabel: gitrepo.Name,
	})

	args = append(args,
		"--targets-file=/run/config/targets.yaml",
		"--label="+bundleLabels.String(),
		"--namespace", gitrepo.Namespace,
		"--service-account", gitrepo.Spec.ServiceAccount,
		fmt.Sprintf("--sync-generation=%d", gitrepo.Spec.ForceSyncGeneration),
		fmt.Sprintf("--paused=%v", gitrepo.Spec.Paused),
		"--target-namespace", gitrepo.Spec.TargetNamespace,
	)

	if gitrepo.Spec.KeepResources {
		args = append(args, "--keep-resources")
	}

	if gitrepo.Spec.CorrectDrift != nil && gitrepo.Spec.CorrectDrift.Enabled {
		args = append(args, "--correct-drift")
		if gitrepo.Spec.CorrectDrift.Force {
			args = append(args, "--correct-drift-force")
		}
		if gitrepo.Spec.CorrectDrift.KeepFailHistory {
			args = append(args, "--correct-drift-keep-fail-history")
		}
	}

	env := []corev1.EnvVar{
		{
			Name:  "HOME",
			Value: fleetHomeDir,
		},
	}
	if gitrepo.Spec.HelmSecretNameForPaths != "" {
		helmArgs := []string{
			"--helm-credentials-by-path-file",
			"/etc/fleet/helm/secrets-path.yaml",
		}

		args = append(args, helmArgs...)
		env = append(env,
			// for ssh go-getter, make sure we always accept new host key
			corev1.EnvVar{
				Name:  "GIT_SSH_COMMAND",
				Value: "ssh -o stricthostkeychecking=accept-new",
			},
		)
	} else if gitrepo.Spec.HelmSecretName != "" {
		helmArgs := []string{
			"--password-file",
			"/etc/fleet/helm/password",
			"--cacerts-file",
			"/etc/fleet/helm/cacerts",
			"--ssh-privatekey-file",
			"/etc/fleet/helm/ssh-privatekey",
		}
		if gitrepo.Spec.HelmRepoURLRegex != "" {
			helmArgs = append(helmArgs, "--helm-repo-url-regex", gitrepo.Spec.HelmRepoURLRegex)
		}
		args = append(args, helmArgs...)
		env = append(env,
			// for ssh go-getter, make sure we always accept new host key
			corev1.EnvVar{
				Name:  "GIT_SSH_COMMAND",
				Value: "ssh -o stricthostkeychecking=accept-new",
			},
			corev1.EnvVar{
				Name: "HELM_USERNAME",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Optional: &[]bool{true}[0],
						Key:      "username",
						LocalObjectReference: corev1.LocalObjectReference{
							Name: gitrepo.Spec.HelmSecretName,
						},
					},
				},
			})
	}

	return append(args, "--", gitrepo.Name), env
}
