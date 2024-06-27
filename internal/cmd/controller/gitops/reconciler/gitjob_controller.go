package reconciler

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	"github.com/rancher/fleet/internal/cmd/controller/grutil"
	"github.com/rancher/fleet/internal/cmd/controller/imagescan"
	"github.com/rancher/fleet/internal/metrics"
	"github.com/rancher/fleet/internal/ociwrapper"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/git"
	"github.com/rancher/fleet/pkg/sharding"
	"github.com/reugn/go-quartz/quartz"

	"github.com/rancher/wrangler/v2/pkg/name"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	bundleCAVolumeName        = "additional-ca"
	bundleCAFile              = "additional-ca.crt"
	gitCredentialVolumeName   = "git-credential" // #nosec G101 this is not a credential
	ociRegistryAuthVolumeName = "oci-auth"
	gitClonerVolumeName       = "git-cloner"
	emptyDirVolumeName        = "git-cloner-empty-dir"
	fleetHomeDir              = "/fleet-home"
)

var two = int32(2)

type GitPoller interface {
	AddOrModifyGitRepoPollJob(ctx context.Context, gitRepo v1alpha1.GitRepo)
	CleanUpGitRepoPollJobs(ctx context.Context)
}

// CronJobReconciler reconciles a GitRepo resource to create a git cloning k8s job
type GitJobReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Image     string
	GitPoller GitPoller
	Scheduler quartz.Scheduler
	Workers   int
	ShardID   string
}

func (r *GitJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.GitRepo{},
			builder.WithPredicates(
				// do not trigger for GitRepo status changes (except for commit changes)
				predicate.Or(
					predicate.GenerationChangedPredicate{},
					predicate.AnnotationChangedPredicate{},
					predicate.LabelChangedPredicate{},
					commitChangedPredicate(),
				),
			),
		).
		Owns(&batchv1.Job{}).
		Watches(
			// Fan out from bundle to gitrepo
			&v1alpha1.Bundle{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, a client.Object) []ctrl.Request {
				repo := a.GetLabels()[v1alpha1.RepoLabel]
				if repo != "" {
					return []ctrl.Request{{
						NamespacedName: types.NamespacedName{
							Namespace: a.GetNamespace(),
							Name:      repo,
						},
					}}
				}

				return []ctrl.Request{}
			}),
			builder.WithPredicates(bundleStatusChangedPredicate()),
		).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// The Reconcile function compares the state specified by
// the GitRepo object against the actual cluster state, and then
// performs operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *GitJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("gitjob")
	gitrepo := &v1alpha1.GitRepo{}

	if err := r.Get(ctx, req.NamespacedName, gitrepo); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	} else if errors.IsNotFound(err) {
		logger.V(1).Info("Gitrepo deleted, cleaning up poll jobs")
		r.GitPoller.CleanUpGitRepoPollJobs(ctx)
		return ctrl.Result{}, nil
	}

	// Restrictions / Overrides, gitrepo reconciler is responsible for setting error in status
	oldStatus := gitrepo.Status.DeepCopy()
	gitrepo, err := grutil.AuthorizeAndAssignDefaults(ctx, r.Client, gitrepo)
	if err != nil {
		return ctrl.Result{}, grutil.UpdateErrorStatus(ctx, r.Client, req.NamespacedName, *oldStatus, err)
	}

	if !gitrepo.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(gitrepo, finalize.GitRepoFinalizer) {
			if err := r.cleanupGitRepo(ctx, logger, gitrepo); err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	if !controllerutil.ContainsFinalizer(gitrepo, finalize.GitRepoFinalizer) {
		err := r.addGitRepoFinalizer(ctx, req.NamespacedName)
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}
	}

	logger = logger.WithValues("generation", gitrepo.Generation, "commit", gitrepo.Status.Commit)
	ctx = log.IntoContext(ctx, logger)

	logger.V(1).Info("Reconciling GitRepo")

	if gitrepo.Spec.Repo == "" {
		return ctrl.Result{}, nil
	}

	r.GitPoller.AddOrModifyGitRepoPollJob(ctx, *gitrepo)

	var job batchv1.Job
	err = r.Get(ctx, types.NamespacedName{
		Namespace: gitrepo.Namespace,
		Name:      jobName(gitrepo),
	}, &job)
	if err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("error retrieving git job: %w", err)
	}

	// Gitjob handling
	if errors.IsNotFound(err) {
		if gitrepo.Spec.DisablePolling {
			if err := r.updateCommit(ctx, gitrepo); err != nil {
				if errors.IsConflict(err) {
					logger.V(1).Info("conflict updating commit, retrying", "message", err)
					return ctrl.Result{Requeue: true}, nil // just retry, but don't show an error
				}
				return ctrl.Result{}, fmt.Errorf("error updating commit: %v", err)
			}
		}
		if gitrepo.Status.Commit != "" {
			if err := r.validateExternalSecretExist(ctx, gitrepo); err != nil {
				return ctrl.Result{}, grutil.UpdateErrorStatus(ctx, r.Client, req.NamespacedName, gitrepo.Status, err)
			}
			logger.V(1).Info("Creating Git job resources")
			if err := r.createJobRBAC(ctx, gitrepo); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to create RBAC resources for git job: %w", err)
			}
			if err := r.createTargetsConfigMap(ctx, gitrepo); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to create targets config map for git job: %w", err)
			}
			if err := r.createJob(ctx, gitrepo); err != nil {
				return ctrl.Result{}, fmt.Errorf("error creating git job: %w", err)
			}
		}
	} else if gitrepo.Status.Commit != "" {
		if err = r.deleteJobIfNeeded(ctx, gitrepo, &job); err != nil {
			return ctrl.Result{}, fmt.Errorf("error deleting git job: %w", err)
		}
	}

	gitrepo.Status.ObservedGeneration = gitrepo.Generation

	// Refresh the status
	if err = grutil.SetStatusFromGitjob(ctx, r.Client, gitrepo, &job); err != nil {
		return ctrl.Result{}, grutil.UpdateErrorStatus(ctx, r.Client, req.NamespacedName, gitrepo.Status, err)
	}

	err = grutil.SetStatusFromBundleDeployments(ctx, r.Client, gitrepo)
	if err != nil {
		return ctrl.Result{}, grutil.UpdateErrorStatus(ctx, r.Client, req.NamespacedName, gitrepo.Status, err)
	}

	err = grutil.SetStatusFromBundles(ctx, r.Client, gitrepo)
	if err != nil {
		return ctrl.Result{}, grutil.UpdateErrorStatus(ctx, r.Client, req.NamespacedName, gitrepo.Status, err)
	}

	if err = grutil.UpdateDisplayState(gitrepo); err != nil {
		return ctrl.Result{}, grutil.UpdateErrorStatus(ctx, r.Client, req.NamespacedName, gitrepo.Status, err)
	}

	grutil.SetStatusFromResourceKey(ctx, r.Client, gitrepo)

	gitrepo.Status.Display.ReadyBundleDeployments = fmt.Sprintf("%d/%d",
		gitrepo.Status.Summary.Ready,
		gitrepo.Status.Summary.DesiredReady)

	grutil.SetCondition(&gitrepo.Status, nil)

	err = grutil.UpdateStatus(ctx, r.Client, req.NamespacedName, gitrepo.Status)
	if err != nil {
		logger.V(1).Error(err, "Reconcile failed final update to git repo status", "status", gitrepo.Status)

		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *GitJobReconciler) updateCommit(ctx context.Context, gitRepo *v1alpha1.GitRepo) error {
	fetcher := git.NewFetch()
	commit, err := fetcher.LatestCommit(ctx, gitRepo, r.Client)
	if err != nil {
		return err
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gitRepo.Status.Commit = commit
		return r.Status().Update(ctx, gitRepo)
	})
}

func (r *GitJobReconciler) cleanupGitRepo(ctx context.Context, logger logr.Logger, gitrepo *v1alpha1.GitRepo) error {
	// Clean up
	logger.V(1).Info("Gitrepo deleted, deleting bundle, image scans")

	metrics.GitRepoCollector.Delete(gitrepo.Name, gitrepo.Namespace)

	nsName := types.NamespacedName{Name: gitrepo.Name, Namespace: gitrepo.Namespace}
	if err := finalize.PurgeBundles(ctx, r.Client, nsName); err != nil {
		return err
	}

	// remove the job scheduled by imagescan, if any
	_ = r.Scheduler.DeleteJob(imagescan.GitCommitKey(gitrepo.Namespace, gitrepo.Name))

	if err := finalize.PurgeImageScans(ctx, r.Client, nsName); err != nil {
		return err
	}

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Get(ctx, nsName, gitrepo); err != nil {
			return err
		}

		controllerutil.RemoveFinalizer(gitrepo, finalize.GitRepoFinalizer)

		return r.Update(ctx, gitrepo)
	})

	if client.IgnoreNotFound(err) != nil {
		return err
	}

	return nil
}

func (r *GitJobReconciler) addGitRepoFinalizer(ctx context.Context, nsName types.NamespacedName) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gitrepo := &v1alpha1.GitRepo{}
		if err := r.Get(ctx, nsName, gitrepo); err != nil {
			return err
		}

		controllerutil.AddFinalizer(gitrepo, finalize.GitRepoFinalizer)

		return r.Update(ctx, gitrepo)
	})

	if err != nil {
		return client.IgnoreNotFound(err)
	}

	return nil
}

func commitChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldGitJob, ok := e.ObjectOld.(*v1alpha1.GitRepo)
			if !ok {
				return true
			}
			newGitJob, ok := e.ObjectNew.(*v1alpha1.GitRepo)
			if !ok {
				return true
			}

			return oldGitJob.Status.Commit != newGitJob.Status.Commit
		},
	}
}

func (r *GitJobReconciler) createJobRBAC(ctx context.Context, gitrepo *v1alpha1.GitRepo) error {
	// No update needed, values are the same. So we ignore AlreadyExists.
	saName := name.SafeConcatName("git", gitrepo.Name)
	sa := grutil.NewServiceAccount(gitrepo.Namespace, saName)
	if err := controllerutil.SetControllerReference(gitrepo, sa, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, sa); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	role := grutil.NewRole(gitrepo.Namespace, saName)
	if err := controllerutil.SetControllerReference(gitrepo, role, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, role); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	rb := grutil.NewRoleBinding(gitrepo.Namespace, saName)
	if err := controllerutil.SetControllerReference(gitrepo, rb, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, rb); err != nil && !errors.IsAlreadyExists(err) {
		return err
	}

	return nil
}

func (r *GitJobReconciler) createTargetsConfigMap(ctx context.Context, gitrepo *v1alpha1.GitRepo) error {
	configMap, err := grutil.NewTargetsConfigMap(gitrepo)
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

func (r *GitJobReconciler) validateExternalSecretExist(ctx context.Context, gitrepo *v1alpha1.GitRepo) error {
	if gitrepo.Spec.HelmSecretNameForPaths != "" {
		if err := r.Get(ctx, types.NamespacedName{Namespace: gitrepo.Namespace, Name: gitrepo.Spec.HelmSecretNameForPaths}, &corev1.Secret{}); err != nil {
			return fmt.Errorf("failed to look up HelmSecretNameForPaths, error: %w", err)
		}
	} else if gitrepo.Spec.HelmSecretName != "" {
		if err := r.Get(ctx, types.NamespacedName{Namespace: gitrepo.Namespace, Name: gitrepo.Spec.HelmSecretName}, &corev1.Secret{}); err != nil {
			return fmt.Errorf("failed to look up helmSecretName, error: %w", err)
		}
	}
	return nil
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

func (r *GitJobReconciler) deleteJobIfNeeded(ctx context.Context, gitRepo *v1alpha1.GitRepo, job *batchv1.Job) error {
	logger := log.FromContext(ctx)
	// if force delete is set, delete the job to make sure a new job is created
	if gitRepo.Spec.ForceSyncGeneration != gitRepo.Status.UpdateGeneration {
		gitRepo.Status.UpdateGeneration = gitRepo.Spec.ForceSyncGeneration
		logger.Info("job deletion triggered because of ForceUpdateGeneration")
		if err := r.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil && !errors.IsNotFound(err) {
			return err
		}
	}

	// k8s Jobs are immutable. Recreate the job if the GitRepo Spec has changed.
	if gitRepo.Generation != gitRepo.Status.ObservedGeneration {
		gitRepo.Status.ObservedGeneration = gitRepo.Generation
		logger.Info("job deletion triggered because of generation change")
		if err := r.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil && !errors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func jobName(obj *v1alpha1.GitRepo) string {
	return name.SafeConcatName(obj.Name, name.Hex(obj.Spec.Repo+obj.Status.Commit, 5))
}

func caBundleName(obj *v1alpha1.GitRepo) string {
	return fmt.Sprintf("%s-cabundle", obj.Name)
}

func (r *GitJobReconciler) newGitJob(ctx context.Context, obj *v1alpha1.GitRepo) (*batchv1.Job, error) {
	jobSpec, err := r.newJobSpec(ctx, obj)
	if err != nil {
		return nil, err
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"generation": strconv.Itoa(int(obj.Generation)),
				"commit":     obj.Status.Commit,
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

	initContainer, err := r.newGitCloner(ctx, obj)
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

	if obj.Spec.CABundle != nil {
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
	}

	for i := range job.Spec.Template.Spec.Containers {
		job.Spec.Template.Spec.Containers[i].VolumeMounts = append(job.Spec.Template.Spec.Containers[i].VolumeMounts, corev1.VolumeMount{
			MountPath: "/workspace/source",
			Name:      gitClonerVolumeName,
		})
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
	paths := gitrepo.Spec.Paths
	if len(paths) == 0 {
		paths = []string{"."}
	}

	// compute configmap, needed because its name contains a hash
	configMap, err := grutil.NewTargetsConfigMap(gitrepo)
	if err != nil {
		return nil, err
	}

	volumes, volumeMounts := volumes(configMap.Name)

	if gitrepo.Spec.HelmSecretNameForPaths != "" {
		vols, volMnts := volumesFromSecret(ctx, r.Client,
			gitrepo.Namespace,
			gitrepo.Spec.HelmSecretNameForPaths,
			"helm-secret-by-path",
		)

		volumes = append(volumes, vols...)
		volumeMounts = append(volumeMounts, volMnts...)

	} else if gitrepo.Spec.HelmSecretName != "" {
		vols, volMnts := volumesFromSecret(ctx, r.Client,
			gitrepo.Namespace,
			gitrepo.Spec.HelmSecretName,
			"helm-secret",
		)

		volumes = append(volumes, vols...)
		volumeMounts = append(volumeMounts, volMnts...)
	}

	if ociwrapper.ExperimentalOCIIsEnabled() && gitrepo.Spec.OCIRegistry != nil && gitrepo.Spec.OCIRegistry.AuthSecretName != "" {
		vol, volMnt, err := ociVolumeFromSecret(ctx, r.Client,
			gitrepo.Namespace,
			gitrepo.Spec.OCIRegistry.AuthSecretName,
			ociRegistryAuthVolumeName,
		)
		if err != nil {
			return nil, err
		}

		volumes = append(volumes, vol)
		volumeMounts = append(volumeMounts, volMnt)
	}

	saName := name.SafeConcatName("git", gitrepo.Name)
	logger := log.FromContext(ctx)
	args, envs := argsAndEnvs(gitrepo, logger.V(1).Enabled())

	return &batchv1.JobSpec{
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
				NodeSelector: map[string]string{"kubernetes.io/os": "linux"},
				Tolerations: []corev1.Toleration{{
					Key:      "cattle.io/os",
					Operator: "Equal",
					Value:    "linux",
					Effect:   "NoSchedule",
				}},
			},
		},
	}, nil
}

func argsAndEnvs(gitrepo *v1alpha1.GitRepo, debug bool) ([]string, []corev1.EnvVar) {
	args := []string{
		"fleet",
		"apply",
	}

	if debug {
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

	if ociwrapper.ExperimentalOCIIsEnabled() && gitrepo.Spec.OCIRegistry != nil && gitrepo.Spec.OCIRegistry.Reference != "" {
		args = append(args, "--oci-reference", gitrepo.Spec.OCIRegistry.Reference)
		if gitrepo.Spec.OCIRegistry.AuthSecretName != "" {
			args = append(args, "--oci-password-file", "/etc/fleet/oci/password")
			env = append(env,
				corev1.EnvVar{
					Name: "OCI_USERNAME",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							Optional: &[]bool{true}[0],
							Key:      "username",
							LocalObjectReference: corev1.LocalObjectReference{
								Name: gitrepo.Spec.OCIRegistry.AuthSecretName,
							},
						},
					},
				})
		}
		if gitrepo.Spec.OCIRegistry.BasicHTTP {
			args = append(args, "--oci-basic-http")
		}
		if gitrepo.Spec.OCIRegistry.InsecureSkipTLS {
			args = append(args, "--oci-insecure")
		}
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

// ociVolumeFromSecret generates a volume and volume mount from a basic-auth secret.
func ociVolumeFromSecret(
	ctx context.Context,
	c client.Client,
	namespace, secretName, volumeName string,
) (corev1.Volume, corev1.VolumeMount, error) {
	var secret corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      secretName,
	}, &secret); err != nil {
		return corev1.Volume{}, corev1.VolumeMount{}, err
	}
	volume := corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: secretName,
			},
		},
	}
	volumeMount := corev1.VolumeMount{
		Name:      volumeName,
		MountPath: "/etc/fleet/oci",
	}
	return volume, volumeMount, nil
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

func (r *GitJobReconciler) newGitCloner(ctx context.Context, obj *v1alpha1.GitRepo) (corev1.Container, error) {
	args := []string{"gitcloner", obj.Spec.Repo, "/workspace"}
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

	if obj.Spec.ClientSecretName != "" {
		var secret corev1.Secret
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: obj.Namespace,
			Name:      obj.Spec.ClientSecretName,
		}, &secret); err != nil {
			return corev1.Container{}, err
		}

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
			knownHosts := secret.Data["known_hosts"]
			if knownHosts != nil {
				args = append(args, "--known-hosts-file", "/gitjob/ssh/known_hosts")
			}
		}
	}

	if obj.Spec.InsecureSkipTLSverify {
		args = append(args, "--insecure-skip-tls")
	}
	if obj.Spec.CABundle != nil {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      bundleCAVolumeName,
			MountPath: "/gitjob/cabundle",
		})
		args = append(args, "--ca-bundle-file", "/gitjob/cabundle/"+bundleCAFile)
	}

	return corev1.Container{
		Command: []string{
			"fleet",
		},
		Args:         args,
		Image:        r.Image,
		Name:         "gitcloner-initializer",
		VolumeMounts: volumeMounts,
		Env:          proxyEnvVars(),
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

func proxyEnvVars() []corev1.EnvVar {
	var envVars []corev1.EnvVar
	for _, envVar := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		if val, ok := os.LookupEnv(envVar); ok {
			envVars = append(envVars, corev1.EnvVar{Name: envVar, Value: val})
		}
	}

	return envVars
}

// bundleStatusChangedPredicate returns true if the bundle
// status has changed, or the bundle was created
func bundleStatusChangedPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			n, isBundle := e.ObjectNew.(*v1alpha1.Bundle)
			if !isBundle {
				return false
			}
			o := e.ObjectOld.(*v1alpha1.Bundle)
			if n == nil || o == nil {
				return false
			}
			return !reflect.DeepEqual(n.Status, o.Status)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return true
		},
	}
}
