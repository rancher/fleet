package controller

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/go-logr/logr"

	v1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	"github.com/rancher/wrangler/v2/pkg/condition"
	"github.com/rancher/wrangler/v2/pkg/kstatus"
	"github.com/rancher/wrangler/v2/pkg/name"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/cli-utils/pkg/kstatus/status"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	bundleCAVolumeName      = "additional-ca"
	bundleCAFile            = "additional-ca.crt"
	gitCredentialVolumeName = "git-credential" // #nosec G101 this is not a credential
	gitClonerVolumeName     = "git-cloner"
	emptyDirVolumeName      = "git-cloner-empty-dir"
)

type GitPoller interface {
	AddOrModifyGitRepoWatch(ctx context.Context, gitJob v1.GitJob)
	CleanUpWatches(ctx context.Context)
}

// CronJobReconciler reconciles a GitJob object
type GitJobReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Image     string
	GitPoller GitPoller
	Log       logr.Logger
}

func (r *GitJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.GitJob{}).
		WithEventFilter(generationOrCommitChangedPredicate()).
		Owns(&batchv1.Job{}).
		Complete(r)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// The Reconcile function compares the state specified by
// the GitJob object against the actual cluster state, and then
// performs operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *GitJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var gitJob v1.GitJob

	if err := r.Get(ctx, req.NamespacedName, &gitJob); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	} else if errors.IsNotFound(err) {
		r.GitPoller.CleanUpWatches(ctx)
		return ctrl.Result{}, nil
	}

	r.GitPoller.AddOrModifyGitRepoWatch(ctx, gitJob)

	var job batchv1.Job
	err := r.Get(ctx, types.NamespacedName{
		Namespace: gitJob.Namespace,
		Name:      jobName(&gitJob),
	}, &job)
	if err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("error retrieving gitJob: %v", err)
	}

	if errors.IsNotFound(err) && gitJob.Status.Commit != "" {
		if err := r.createJob(ctx, &gitJob); err != nil {
			return ctrl.Result{}, fmt.Errorf("error creating job: %v", err)
		}
	} else {
		if err = r.updateStatus(ctx, &gitJob, &job); err != nil {
			return ctrl.Result{}, fmt.Errorf("error updating gitjob status: %v", err)
		}
		if err = r.deleteJobIfNeeded(ctx, &gitJob, &job); err != nil {
			return ctrl.Result{}, fmt.Errorf("error deleting job: %v", err)
		}
	}

	return ctrl.Result{}, nil
}

func generationOrCommitChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldGitJob, ok := e.ObjectOld.(*v1.GitJob)
			if !ok {
				return true
			}
			newGitJob, ok := e.ObjectNew.(*v1.GitJob)
			if !ok {
				return true
			}

			return oldGitJob.Generation != newGitJob.Generation || oldGitJob.Status.Commit != newGitJob.Status.Commit
		},
	}
}

func (r *GitJobReconciler) createJob(ctx context.Context, gitJob *v1.GitJob) error {
	job, err := r.newJob(ctx, gitJob)
	if err != nil {
		return err
	}
	if err := controllerutil.SetControllerReference(gitJob, job, r.Scheme); err != nil {
		return err
	}
	err = r.Create(ctx, job)
	if err != nil {
		return err
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var gitJobFomCluster v1.GitJob
		err := r.Get(ctx, types.NamespacedName{Name: gitJob.Name, Namespace: gitJob.Namespace}, &gitJobFomCluster)
		if err != nil {
			return err
		}
		gitJobFomCluster.Status.ObservedGeneration = gitJobFomCluster.Generation
		gitJobFomCluster.Status.LastSyncedTime = metav1.Now()

		return r.Status().Update(ctx, &gitJobFomCluster)
	})

}

func (r *GitJobReconciler) updateStatus(ctx context.Context, gitJob *v1.GitJob, job *batchv1.Job) error {
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(job)
	if err != nil {
		return err
	}
	uJob := &unstructured.Unstructured{Object: obj}
	result, err := status.Compute(uJob)
	if err != nil {
		return err
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var gitJobFomCluster v1.GitJob

		err := r.Get(ctx, types.NamespacedName{Name: gitJob.Name, Namespace: gitJob.Namespace}, &gitJobFomCluster)
		if err != nil {
			return err
		}

		gitJobFomCluster.Status.JobStatus = result.Status.String()
		for _, con := range result.Conditions {
			condition.Cond(con.Type.String()).SetStatus(&gitJobFomCluster, string(con.Status))
			condition.Cond(con.Type.String()).SetMessageIfBlank(&gitJobFomCluster, con.Message)
			condition.Cond(con.Type.String()).Reason(&gitJobFomCluster, con.Reason)
		}

		if result.Status == status.FailedStatus {
			selector := labels.SelectorFromSet(labels.Set{
				"job-name": job.Name,
			})
			var podList corev1.PodList
			err := r.Client.List(ctx, &podList, &client.ListOptions{LabelSelector: selector})
			if err != nil {
				return err
			}
			sort.Slice(podList.Items, func(i, j int) bool {
				return podList.Items[i].CreationTimestamp.Before(&podList.Items[j].CreationTimestamp)
			})
			terminationMessage := result.Message
			if len(podList.Items) > 0 {
				for _, podStatus := range podList.Items[len(podList.Items)-1].Status.ContainerStatuses {
					if podStatus.Name != "step-git-source" && podStatus.State.Terminated != nil {
						terminationMessage += podStatus.State.Terminated.Message
					}
				}
			}
			kstatus.SetError(&gitJobFomCluster, terminationMessage)
		}

		if result.Status == status.CurrentStatus {
			if strings.Contains(result.Message, "Job Completed") {
				gitJobFomCluster.Status.LastExecutedCommit = job.Annotations["commit"]
			}
			kstatus.SetActive(&gitJobFomCluster)
		}

		gitJobFomCluster.Status.ObservedGeneration = gitJobFomCluster.Generation
		gitJobFomCluster.Status.LastSyncedTime = metav1.Now()

		return r.Status().Update(ctx, &gitJobFomCluster)
	})
}

func (r *GitJobReconciler) deleteJobIfNeeded(ctx context.Context, gitJob *v1.GitJob, job *batchv1.Job) error {
	// if force delete is set, delete the job to make sure a new job is created
	if gitJob.Spec.ForceUpdateGeneration != gitJob.Status.UpdateGeneration {
		gitJob.Status.UpdateGeneration = gitJob.Spec.ForceUpdateGeneration
		r.Log.Info("job deletion triggered because of ForceUpdateGeneration")
		if err := r.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil && !errors.IsNotFound(err) {
			return err
		}
	}

	// if the job failed, e.g. because a helm registry was unreachable, delete the old job
	if isJobError(gitJob) && gitJob.Generation != gitJob.Status.ObservedGeneration {
		r.Log.Info("job deletion triggered because of generation has changed, and it was in an error state")
		if err := r.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil && !errors.IsNotFound(err) {
			return err
		}
	}

	return nil
}

func jobName(obj *v1.GitJob) string {
	return name.SafeConcatName(obj.Name, name.Hex(obj.Spec.Git.Repo+obj.Status.Commit, 5))
}

func caBundleName(obj *v1.GitJob) string {
	return fmt.Sprintf("%s-cabundle", obj.Name)
}

func (r *GitJobReconciler) newJob(ctx context.Context, obj *v1.GitJob) (*batchv1.Job, error) {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"generation": strconv.Itoa(int(obj.Generation)),
				"commit":     obj.Status.Commit,
			},
			Namespace: obj.Namespace,
			Name:      jobName(obj),
		},
		Spec: obj.Spec.JobSpec,
	}

	initContainer, err := r.generateInitContainer(ctx, obj)
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

	if obj.Spec.Git.CABundle != nil {
		job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes, corev1.Volume{
			Name: bundleCAVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: caBundleName(obj),
				},
			},
		})
	}

	if obj.Spec.Git.ClientSecretName != "" {
		job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: gitCredentialVolumeName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: obj.Spec.Git.ClientSecretName,
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
			corev1.EnvVar{
				Name:  "EVENT_TYPE",
				Value: obj.Status.Event,
			},
		)
		job.Spec.Template.Spec.Containers[i].Env = append(job.Spec.Template.Spec.Containers[i].Env, proxyEnvVars()...)
	}

	return job, nil
}

func (r *GitJobReconciler) generateInitContainer(ctx context.Context, obj *v1.GitJob) (corev1.Container, error) {
	args := []string{obj.Spec.Git.Repo, "/workspace"}
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
	if obj.Spec.Git.Branch != "" {
		args = append(args, "--branch", obj.Spec.Git.Branch)
	} else if obj.Spec.Git.Revision != "" {
		args = append(args, "--revision", obj.Spec.Git.Revision)
	}

	if obj.Spec.Git.ClientSecretName != "" {
		var secret corev1.Secret
		if err := r.Get(ctx, types.NamespacedName{
			Namespace: obj.Namespace,
			Name:      obj.Spec.Git.ClientSecretName,
		}, &secret); err != nil {
			return corev1.Container{}, err
		}

		if secret.Type == corev1.SecretTypeBasicAuth {
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      gitCredentialVolumeName,
				MountPath: "/gitjob/credentials",
			})
			args = append(args, "--username", string(secret.Data[corev1.BasicAuthUsernameKey]))
			args = append(args, "--password-file", "/gitjob/credentials/"+corev1.BasicAuthPasswordKey)
		} else if secret.Type == corev1.SecretTypeSSHAuth {
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

	if obj.Spec.Git.InsecureSkipTLSverify {
		args = append(args, "--insecure-skip-tls")
	}
	if obj.Spec.Git.CABundle != nil {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      bundleCAVolumeName,
			MountPath: "/gitjob/cabundle",
		})
		args = append(args, "--ca-bundle-file", "/gitjob/cabundle/"+bundleCAFile)
	}

	return corev1.Container{
		Command: []string{
			"gitcloner",
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

// isJobError returns true if the conditions from kstatus.SetError, used by job controller, are matched
func isJobError(obj *v1.GitJob) bool {
	return kstatus.Reconciling.IsFalse(obj) && kstatus.Stalled.IsTrue(obj) && obj.Status.JobStatus == status.FailedStatus.String()
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
