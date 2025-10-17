package reconciler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	fleetapply "github.com/rancher/fleet/internal/cmd/cli/apply"
	"github.com/rancher/fleet/internal/config"
	fleetgithub "github.com/rancher/fleet/internal/github"
	"github.com/rancher/fleet/internal/names"
	"github.com/rancher/fleet/internal/ocistorage"
	ssh "github.com/rancher/fleet/internal/ssh"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/cert"
	fleetevent "github.com/rancher/fleet/pkg/event"
	"github.com/rancher/fleet/pkg/sharding"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	bundleCAVolumeName        = "additional-ca"
	bundleCAFile              = "additional-ca.crt"
	gitCredentialVolumeName   = "git-credential" // #nosec G101 this is not a credential
	ociRegistryAuthVolumeName = "oci-auth"
	gitClonerVolumeName       = "git-cloner"
	emptyDirVolumeName        = "git-cloner-empty-dir"

	fleetHomeDir = "/fleet-home"

	bundleOptionsSeparatorChars = ":,|?<>"
)

type helmSecretOptions struct {
	HasCACerts      bool
	InsecureSkipTLS bool
	BasicHTTP       bool
}

func (r *GitJobReconciler) createJobAndResources(ctx context.Context, gitrepo *v1alpha1.GitRepo, logger logr.Logger) error {
	logger.V(1).Info("Creating Git job resources")

	if err := r.createJobRBAC(ctx, gitrepo); err != nil {
		return fmt.Errorf("failed to create RBAC resources for git job: %w", err)
	}
	if err := r.createTargetsConfigMap(ctx, gitrepo); err != nil {
		return fmt.Errorf("failed to create targets config map for git job: %w", err)
	}
	if _, err := r.createCABundleSecret(ctx, gitrepo, caBundleName(gitrepo)); err != nil {
		return fmt.Errorf("failed to create cabundle secret for git job: %w", err)
	}
	if err := r.createJob(ctx, gitrepo); err != nil {
		return fmt.Errorf("error creating git job: %w", err)
	}

	r.Recorder.Event(gitrepo, fleetevent.Normal, "Created", "GitJob was created")
	return nil
}

func (r *GitJobReconciler) createTargetsConfigMap(ctx context.Context, gitrepo *v1alpha1.GitRepo) error {
	configMap, err := newTargetsConfigMap(gitrepo)
	if err != nil {
		return err
	}
	if err := controllerutil.SetControllerReference(gitrepo, configMap, r.Scheme); err != nil {
		return err
	}
	data := configMap.BinaryData
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, configMap, func() error {
		configMap.BinaryData = data
		return nil
	})

	return err
}

// createCABundleSecret creates a CA bundle secret, if the provided source contains data.
// That provided source may be the CABundle field of the provided gitrepo (if the provided name matches the CA bundle
// name expected for the gitrepo, and that CABundle field is non-empty), or Rancher-configured secrets in all other cases.
// This returns a boolean indicating whether the secret has been successfully created (or updated, in case it already
// existed), and an error.
func (r *GitJobReconciler) createCABundleSecret(ctx context.Context, gitrepo *v1alpha1.GitRepo, name string) (bool, error) {
	var caBundle []byte
	fieldName := "cacerts"

	if name == caBundleName(gitrepo) {
		caBundle = gitrepo.Spec.CABundle
		fieldName = bundleCAFile
	}

	if len(caBundle) == 0 {
		cab, err := cert.GetRancherCABundle(ctx, r.Client)
		if err != nil {
			return false, err
		}

		if len(cab) == 0 {
			return false, nil
		}

		caBundle = cab
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: gitrepo.Namespace,
			Name:      name,
		},
		Data: map[string][]byte{
			fieldName:            caBundle,
			"insecureSkipVerify": fmt.Appendf([]byte{}, "%t", gitrepo.Spec.InsecureSkipTLSverify),
		},
	}
	if err := controllerutil.SetControllerReference(gitrepo, secret, r.Scheme); err != nil {
		return false, err
	}
	data := secret.StringData
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		secret.StringData = data // Supports update case, if the secret already exists.
		return nil
	})

	return true, err
}

func (r *GitJobReconciler) createJob(ctx context.Context, gitRepo *v1alpha1.GitRepo) error {
	job, err := r.newGitJob(ctx, gitRepo)
	if err != nil {
		return err
	}
	if err := controllerutil.SetControllerReference(gitRepo, job, r.Scheme); err != nil {
		return err
	}
	return r.Create(ctx, job)
}

func (r *GitJobReconciler) newGitJob(ctx context.Context, obj *v1alpha1.GitRepo) (*batchv1.Job, error) {
	jobSpec, err := r.newJobSpec(ctx, obj)
	if err != nil {
		return nil, err
	}
	var fleetControllerDeployment appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: r.SystemNamespace,
		Name:      config.ManagerConfigName,
	}, &fleetControllerDeployment); err != nil {
		return nil, err
	}

	// add tolerations from the fleet-controller deployment
	jobSpec.Template.Spec.Tolerations = append(
		jobSpec.Template.Spec.Tolerations,
		fleetControllerDeployment.Spec.Template.Spec.Tolerations...,
	)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"generation": strconv.Itoa(int(obj.Generation)),
				"commit":     obj.Status.Commit,
			},
			Labels: map[string]string{
				forceSyncGenerationLabel: fmt.Sprintf("%d", obj.Spec.ForceSyncGeneration),
				generationLabel:          fmt.Sprintf("%d", obj.Generation),
			},
			Namespace: obj.Namespace,
			Name:      jobName(obj),
		},
		Spec: *jobSpec,
	}
	// if the repo references a shard, add the same label to the job
	// this avoids a call to Reconcile for controllers that do not match
	// the shard-id
	label, hasLabel := obj.GetLabels()[sharding.ShardingRefLabel]
	if hasLabel {
		job.Labels = labels.Merge(job.Labels, map[string]string{
			sharding.ShardingRefLabel: label,
		})
	}

	knownHostsData, err := r.KnownHosts.Get(ctx, r.Client, obj.Namespace, obj.Spec.ClientSecretName)
	if err != nil {
		return nil, err
	}

	initContainer, err := r.newGitCloner(ctx, obj, knownHostsData)
	if err != nil {
		return nil, err
	}

	job.Spec.Template.Spec.InitContainers = []corev1.Container{initContainer}
	job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes,
		corev1.Volume{
			Name: gitClonerVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		}, corev1.Volume{
			Name: emptyDirVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	)

	// Look for a `--ca-bundle-file` arg to the git cloner. This applies to cases where the GitRepo's `Spec.CABundle` is
	// specified, but also to cases where a CA bundle secret has been created instead, with data from Rancher
	// secrets.
	if slices.Contains(initContainer.Args, "--ca-bundle-file") {
		job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes, corev1.Volume{
			Name: bundleCAVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: caBundleName(obj),
				},
			},
		})
	}

	if obj.Spec.ClientSecretName != "" {
		job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: gitCredentialVolumeName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: obj.Spec.ClientSecretName,
					},
				},
			},
		)
	} else {
		// Create a volume for the default credentials secret if it exists
		var secret corev1.Secret
		err := r.Get(ctx, types.NamespacedName{
			Namespace: obj.Namespace,
			Name:      config.DefaultGitCredentialsSecretName,
		}, &secret)

		if err != nil && !apierrors.IsNotFound(err) {
			return nil, err
		}

		if err == nil {
			job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes,
				corev1.Volume{
					Name: gitCredentialVolumeName,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: config.DefaultGitCredentialsSecretName,
						},
					},
				},
			)
		}
	}

	for i := range job.Spec.Template.Spec.Containers {
		job.Spec.Template.Spec.Containers[i].VolumeMounts = append(job.Spec.Template.Spec.Containers[i].VolumeMounts, corev1.VolumeMount{
			MountPath: "/workspace/source",
			Name:      gitClonerVolumeName,
		})

		if knownHostsData != "" {
			job.Spec.Template.Spec.Containers[i].Env = append(
				job.Spec.Template.Spec.Containers[i].Env,
				corev1.EnvVar{Name: ssh.KnownHostsEnvVar, Value: knownHostsData},
			)
		}

		job.Spec.Template.Spec.Containers[i].Env = append(job.Spec.Template.Spec.Containers[i].Env,
			corev1.EnvVar{
				Name:  "COMMIT",
				Value: obj.Status.Commit,
			},
		)
		job.Spec.Template.Spec.Containers[i].Env = append(job.Spec.Template.Spec.Containers[i].Env, proxyEnvVars()...)
	}

	return job, nil
}

func (r *GitJobReconciler) newJobSpec(ctx context.Context, gitrepo *v1alpha1.GitRepo) (*batchv1.JobSpec, error) {
	var CACertsFilePathOverride string

	paths := gitrepo.Spec.Paths
	if len(paths) == 0 {
		paths = []string{"."}
	}

	drivenScanSeparator := ""
	var err error
	if len(gitrepo.Spec.Bundles) > 0 {
		paths = []string{}
		// use driven scan instead
		// We calculate a separator because we will continue using the
		// same call format for "fleet apply."
		// The bundle definitions + options file (fleet.yaml)
		// will be passed at the end, in the same way we pass the bundle
		// directories for the classic fleet scan, but since we need to
		// pass 2 strings, we will separate them with
		// the calculated separator.
		drivenScanSeparator, err = getDrivenScanSeparator(*gitrepo)
		if err != nil {
			return nil, err
		}
		for _, b := range gitrepo.Spec.Bundles {
			path := b.Base
			if b.Options != "" {
				path = path + drivenScanSeparator + b.Options
			}
			paths = append(paths, path)
		}
	}

	// compute configmap, needed because its name contains a hash
	configMap, err := newTargetsConfigMap(gitrepo)
	if err != nil {
		return nil, err
	}

	volumes, volumeMounts := volumes(configMap.Name)
	var certVolCreated bool
	var helmInsecure bool
	var helmBasicHTTP bool

	if gitrepo.Spec.HelmSecretNameForPaths != "" {
		vols, volMnts, helmSecretOpts := volumesFromSecret(ctx, r.Client,
			gitrepo.Namespace,
			gitrepo.Spec.HelmSecretNameForPaths,
			"helm-secret-by-path",
			"",
		)

		certVolCreated = helmSecretOpts.HasCACerts
		helmInsecure = helmSecretOpts.InsecureSkipTLS

		volumes = append(volumes, vols...)
		volumeMounts = append(volumeMounts, volMnts...)

	} else if gitrepo.Spec.HelmSecretName != "" {
		vols, volMnts, helmSecretOpts := volumesFromSecret(ctx, r.Client,
			gitrepo.Namespace,
			gitrepo.Spec.HelmSecretName,
			"helm-secret",
			"",
		)

		certVolCreated = helmSecretOpts.HasCACerts
		helmInsecure = helmSecretOpts.InsecureSkipTLS
		helmBasicHTTP = helmSecretOpts.BasicHTTP

		volumes = append(volumes, vols...)
		volumeMounts = append(volumeMounts, volMnts...)
	}

	// In case no Helm secret volume has been created, because Helm secrets don't exist or don't contain a CA
	// bundle, mount a volume with a Rancher CA bundle.
	if !certVolCreated {
		// Fall back to Rancher-configured secrets
		// We need to copy secret data from Rancher, because Rancher secrets live in a different namespace and
		// can therefore not be used as sources for a volume.
		secretName := rancherCABundleName(gitrepo)
		res, err := r.createCABundleSecret(ctx, gitrepo, secretName)
		if err != nil {
			return nil, err
		}

		if res {
			CACertsDirOverride := "/etc/rancher/certs"

			// Override the volume name and mount path to prevent any conflict with an existing Helm secret
			// providing username and password.
			vols, volMnts, _ := volumesFromSecret(ctx, r.Client,
				gitrepo.Namespace,
				secretName,
				"rancher-helm-secret",
				CACertsDirOverride,
			)

			volumes = append(volumes, vols...)
			volumeMounts = append(volumeMounts, volMnts...)

			CACertsFilePathOverride = CACertsDirOverride + "/cacerts"
		}
	}

	shardID := gitrepo.Labels[sharding.ShardingRefLabel]

	nodeSelector := map[string]string{"kubernetes.io/os": "linux"}
	if shardID != "" && len(strings.TrimSpace(r.JobNodeSelector)) > 0 {
		var shardNodeSelector map[string]string
		if err := json.Unmarshal([]byte(r.JobNodeSelector), &shardNodeSelector); err != nil {
			return nil, fmt.Errorf("could not decode shard node selector: %w", err)
		}

		for k, v := range shardNodeSelector {
			nodeSelector[k] = v
		}
	}

	saName := names.SafeConcatName("git", gitrepo.Name)
	logger := log.FromContext(ctx)
	args, envs := argsAndEnvs(gitrepo, logger, CACertsFilePathOverride, r.KnownHosts, drivenScanSeparator, helmInsecure, helmBasicHTTP)

	zero := int32(0)

	return &batchv1.JobSpec{
		BackoffLimit: &zero,
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
						Name:         "fleet",
						Image:        r.Image,
						Command:      []string{"log.sh"},
						Args:         append(args, paths...),
						WorkingDir:   "/workspace/source",
						VolumeMounts: volumeMounts,
						Env:          envs,
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
				NodeSelector: nodeSelector,
				Tolerations: []corev1.Toleration{
					{
						Key:      "cattle.io/os",
						Operator: "Equal",
						Value:    "linux",
						Effect:   "NoSchedule",
					},
					{
						Key:      "node.cloudprovider.kubernetes.io/uninitialized",
						Operator: "Equal",
						Value:    "true",
						Effect:   "NoSchedule",
					},
				},
			},
		},
	}, nil
}

func (r *GitJobReconciler) newGitCloner(
	ctx context.Context,
	obj *v1alpha1.GitRepo,
	knownHosts string,
) (corev1.Container, error) {
	args := []string{"fleet", "gitcloner", obj.Spec.Repo, "/workspace"}
	volumeMounts := []corev1.VolumeMount{
		{
			Name:      gitClonerVolumeName,
			MountPath: "/workspace",
		},
		{
			Name:      emptyDirVolumeName,
			MountPath: "/tmp",
		},
	}

	branch, rev := obj.Spec.Branch, obj.Spec.Revision
	if branch != "" {
		args = append(args, "--branch", branch)
	} else if rev != "" {
		args = append(args, "--revision", rev)
	} else {
		args = append(args, "--branch", "master")
	}

	secretName := obj.Spec.ClientSecretName
	if secretName == "" {
		secretName = config.DefaultGitCredentialsSecretName
	}

	var secret corev1.Secret
	err := r.Get(ctx, types.NamespacedName{
		Namespace: obj.Namespace,
		Name:      secretName,
	}, &secret)

	if err != nil && secretName == obj.Spec.ClientSecretName {
		// Only error if an explicitly referenced secret was not found;
		// The absence of a default secret might simply mean that no credentials are needed.
		return corev1.Container{}, err
	}

	if err == nil {
		switch secret.Type {
		case corev1.SecretTypeBasicAuth:
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      gitCredentialVolumeName,
				MountPath: "/gitjob/credentials",
			})
			args = append(args, "--username", string(secret.Data[corev1.BasicAuthUsernameKey]))
			args = append(args, "--password-file", "/gitjob/credentials/"+corev1.BasicAuthPasswordKey)
		case corev1.SecretTypeSSHAuth:
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      gitCredentialVolumeName,
				MountPath: "/gitjob/ssh",
			})
			args = append(args, "--ssh-private-key-file", "/gitjob/ssh/"+corev1.SSHAuthPrivateKey)
		default:
			if fleetgithub.HasGitHubAppKeys(&secret) {
				volumeMounts = append(volumeMounts, corev1.VolumeMount{
					Name:      gitCredentialVolumeName,
					MountPath: "/gitjob/githubapp",
				})
				args = append(args,
					"--github-app-id", string(secret.Data[fleetgithub.GithubAppIDKey]),
					"--github-app-installation-id", string(secret.Data[fleetgithub.GithubAppInstallationIDKey]),
					"--github-app-key-file", "/gitjob/githubapp/"+fleetgithub.GithubAppPrivateKeyKey,
				)
			} else {
				return corev1.Container{}, fmt.Errorf("missing Github App keys in secret %s/%s", secret.Namespace, secret.Name)
			}
		}
	}

	if obj.Spec.InsecureSkipTLSverify {
		args = append(args, "--insecure-skip-tls")
	}

	var CABundleSecret corev1.Secret
	err = r.Get(ctx, types.NamespacedName{
		Namespace: obj.Namespace,
		Name:      caBundleName(obj),
	}, &CABundleSecret)
	if client.IgnoreNotFound(err) != nil {
		return corev1.Container{}, err
	}

	if !apierrors.IsNotFound(err) {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      bundleCAVolumeName,
			MountPath: "/gitjob/cabundle",
		})
		args = append(args, "--ca-bundle-file", "/gitjob/cabundle/"+bundleCAFile)
	}

	env := []corev1.EnvVar{
		{
			Name:  fleetapply.JSONOutputEnvVar,
			Value: "true",
		},
	}
	env = append(env, proxyEnvVars()...)

	// If strict host key checks are enabled but no entries are available, another error will be shown by the known
	// hosts getter, as that means that the Fleet deployment is incomplete.
	// On the other hand, we do not want to feed entries to the cloner if strict host key checks are disabled, as that
	// would lead it to unduly reject SSH connection attempts.
	if r.KnownHosts.IsStrict() {
		env = append(env, corev1.EnvVar{Name: ssh.KnownHostsEnvVar, Value: knownHosts})
	}

	return corev1.Container{
		Command:      []string{"log.sh"},
		Args:         args,
		Image:        r.Image,
		Name:         "gitcloner-initializer",
		VolumeMounts: volumeMounts,
		Env:          env,
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: &[]bool{false}[0],
			ReadOnlyRootFilesystem:   &[]bool{true}[0],
			Privileged:               &[]bool{false}[0],
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			RunAsNonRoot:             &[]bool{true}[0],
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
	}, nil
}

func argsAndEnvs(
	gitrepo *v1alpha1.GitRepo,
	logger logr.Logger,
	CACertsPathOverride string,
	knownHosts KnownHostsGetter,
	drivenScanSeparator string,
	helmInsecureSkipTLS bool,
	helmBasicHTTP bool,
) ([]string, []corev1.EnvVar) {
	args := []string{
		"fleet",
		"apply",
	}

	if logger.V(1).Enabled() {
		args = append(args, "--debug", "--debug-level", "9")
	}

	bundleLabels := labels.Merge(gitrepo.Labels, map[string]string{
		v1alpha1.RepoLabel: gitrepo.Name,
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

	if gitrepo.Spec.DeleteNamespace {
		args = append(args, "--delete-namespace")
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

	fleetApplyRetries, err := fleetapply.GetOnConflictRetries()
	if err != nil {
		logger.Error(err, "failed parsing env variable, using defaults", "env_var_name", fleetapply.FleetApplyConflictRetriesEnv)
	}
	env := []corev1.EnvVar{
		{
			Name:  "HOME",
			Value: fleetHomeDir,
		},
		{
			Name:  fleetapply.JSONOutputEnvVar,
			Value: "true",
		},
		{
			Name:  fleetapply.JobNameEnvVar,
			Value: jobName(gitrepo),
		},
		{
			Name:  fleetapply.FleetApplyConflictRetriesEnv,
			Value: strconv.Itoa(fleetApplyRetries),
		},
	}

	if helmInsecureSkipTLS {
		env = append(env, corev1.EnvVar{
			Name:  "GIT_SSL_NO_VERIFY",
			Value: "true",
		})
	}

	if gitrepo.Spec.HelmSecretNameForPaths != "" {
		helmArgs := []string{
			"--helm-credentials-by-path-file",
			"/etc/fleet/helm/secrets-path.yaml",
		}

		args = append(args, helmArgs...)
		// for ssh go-getter
		env = append(env, gitSSHCommandEnvVar(knownHosts.IsStrict()))
	} else if gitrepo.Spec.HelmSecretName != "" {
		helmArgs := []string{
			"--password-file",
			"/etc/fleet/helm/password",
			"--ssh-privatekey-file",
			"/etc/fleet/helm/ssh-privatekey",
		}

		if CACertsPathOverride == "" {
			helmArgs = append(helmArgs,
				"--cacerts-file",
				"/etc/fleet/helm/cacerts",
			)
		}

		if gitrepo.Spec.HelmRepoURLRegex != "" {
			helmArgs = append(helmArgs, "--helm-repo-url-regex", gitrepo.Spec.HelmRepoURLRegex)
		}
		args = append(args, helmArgs...)
		// for ssh go-getter
		env = append(env, gitSSHCommandEnvVar(knownHosts.IsStrict()))
		env = append(env,
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

	if CACertsPathOverride != "" {
		helmArgs := []string{
			"--cacerts-file",
			CACertsPathOverride,
		}
		if gitrepo.Spec.HelmRepoURLRegex != "" {
			helmArgs = append(helmArgs, "--helm-repo-url-regex", gitrepo.Spec.HelmRepoURLRegex)
		}
		args = append(args, helmArgs...)
		env = append(env, gitSSHCommandEnvVar(knownHosts.IsStrict()))
	}

	if !ocistorage.OCIIsEnabled() {
		env = append(env,
			corev1.EnvVar{
				Name:  ocistorage.OCIStorageFlag,
				Value: "false",
			})
	} else {
		args = append(args, "--oci-registry-secret", gitrepo.Spec.OCIRegistrySecret)
	}

	if len(gitrepo.Spec.Bundles) > 0 {
		args = append(args, "--driven-scan")
		if drivenScanSeparator != "" {
			args = append(args, "--driven-scan-sep", drivenScanSeparator)
		}
	}

	if helmInsecureSkipTLS {
		args = append(args, "--helm-insecure-skip-tls")
	}
	if helmBasicHTTP {
		args = append(args, "--helm-basic-http")
	}

	return append(args, "--", gitrepo.Name), env
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
// It also returns a struct containing boolean values indicating if a volume has
// been created for CA bundles, along with values (defaulting to false) of the
// `insecureSkipVerify` and `basicHTTP` keys of the secret.
func volumesFromSecret(
	ctx context.Context,
	c client.Client,
	namespace string,
	secretName, volumeName, mountPath string,
) ([]corev1.Volume, []corev1.VolumeMount, helmSecretOptions) {
	if mountPath == "" {
		mountPath = "/etc/fleet/helm"
	}

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
			MountPath: mountPath,
		},
	}

	// Mount a CA certificate, if specified in the secret. This is necessary to support Helm registries with
	// self-signed certificates.
	secret := &corev1.Secret{}
	var certVolCreated bool
	_ = c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: secretName}, secret)
	if _, ok := secret.Data["cacerts"]; ok {
		volumes = append(volumes, corev1.Volume{
			Name: fmt.Sprintf("%s-cert", volumeName),
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
			Name:      fmt.Sprintf("%s-cert", volumeName),
			MountPath: "/etc/ssl/certs",
		})

		certVolCreated = true
	}

	// Get the values for skipping TLS and basic HTTP connections.
	// In case of error reading the values they will be considered
	// as set to false as those values are security related.
	insecureSkipVerify := false
	if value, ok := secret.Data["insecureSkipVerify"]; ok {
		boolValue, err := strconv.ParseBool(string(value))
		if err == nil {
			insecureSkipVerify = boolValue
		}
	}

	basicHTTP := false
	if value, ok := secret.Data["basicHTTP"]; ok {
		boolValue, err := strconv.ParseBool(string(value))
		if err == nil {
			basicHTTP = boolValue
		}
	}

	secretOpts := helmSecretOptions{
		InsecureSkipTLS: insecureSkipVerify,
		BasicHTTP:       basicHTTP,
		HasCACerts:      certVolCreated,
	}

	return volumes, volumeMounts, secretOpts
}

func proxyEnvVars() []corev1.EnvVar {
	var envVars []corev1.EnvVar
	for _, envVar := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		if val, ok := os.LookupEnv(envVar); ok {
			envVars = append(envVars, corev1.EnvVar{Name: envVar, Value: val})
		}
	}

	return envVars
}

func gitSSHCommandEnvVar(strictChecks bool) corev1.EnvVar {
	strictVal := "no"

	if strictChecks {
		strictVal = "yes"
	}

	return corev1.EnvVar{
		Name:  "GIT_SSH_COMMAND",
		Value: fmt.Sprintf("ssh -o stricthostkeychecking=%s", strictVal),
	}
}

// getDrivenScanSeparator returns a separator that is valid for all the Bundle
// definitions in the given GitRepo.
// Since we cannot disregard the possibility that a user might have an
// unavoidable need to use the character ":" (or another character typically not
// used in directory or file paths), we need to find possible alternatives.
// The function will search for simple characters from those in
// bundleOptionsSeparatorChars, and if none of them can be used, it will return an error.
func getDrivenScanSeparator(gitrepo v1alpha1.GitRepo) (string, error) {
	for _, sep := range bundleOptionsSeparatorChars {
		if !separatorInBundleDefinitions(gitrepo, sep) {
			// We can safely use this separator
			return string(sep), nil
		}
	}

	return "", fmt.Errorf("bundle base and/or options paths contain all possible characters from %q, please update those paths to remedy this", bundleOptionsSeparatorChars)
}

func separatorInBundleDefinitions(gitrepo v1alpha1.GitRepo, sep rune) bool {
	for _, b := range gitrepo.Spec.Bundles {
		if strings.ContainsRune(b.Options, sep) {
			return true
		}

		if strings.ContainsRune(b.Base, sep) {
			return true
		}
	}

	return false
}

func jobName(obj *v1alpha1.GitRepo) string {
	return names.SafeConcatName(obj.Name, names.Hex(obj.Spec.Repo+obj.Status.Commit, 5))
}

func caBundleName(obj *v1alpha1.GitRepo) string {
	return fmt.Sprintf("%s-cabundle", obj.Name)
}

func rancherCABundleName(obj *v1alpha1.GitRepo) string {
	return fmt.Sprintf("%s-rancher-cabundle", obj.Name)
}
