package gitjob

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	v1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	v1controller "github.com/rancher/gitjob/pkg/generated/controllers/gitjob.cattle.io/v1"
	"github.com/rancher/gitjob/pkg/git"
	"github.com/rancher/gitjob/pkg/types"
	"github.com/rancher/wrangler/v2/pkg/apply"
	"github.com/sirupsen/logrus"

	batchv1controller "github.com/rancher/wrangler/v2/pkg/generated/controllers/batch/v1"
	corev1controller "github.com/rancher/wrangler/v2/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/v2/pkg/kstatus"
	"github.com/rancher/wrangler/v2/pkg/name"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	types2 "k8s.io/apimachinery/pkg/types"
)

const (
	bundleCAVolumeName      = "additional-ca"
	bundleCAFile            = "additional-ca.crt"
	gitCredentialVolumeName = "git-credential" // #nosec G101 this is not a credential
	gitClonerVolumeName     = "git-cloner"
	emptyDirVolumeName      = "git-cloner-empty-dir"
)

func Register(ctx context.Context, cont *types.Context) {
	h := Handler{
		image:   cont.Image,
		ctx:     ctx,
		gitjobs: cont.Gitjob.Gitjob().V1().GitJob(),
		secrets: cont.Core.Core().V1().Secret().Cache(),
		batch:   cont.Batch.Batch().V1().Job(),
	}

	v1controller.RegisterGitJobGeneratingHandler(
		ctx,
		cont.Gitjob.Gitjob().V1().GitJob(),
		cont.Apply.
			WithSetOwnerReference(true, false).
			WithDynamicLookup().
			WithCacheTypes(cont.Core.Core().V1().Secret()).
			WithPatcher(
				batchv1.SchemeGroupVersion.WithKind("Job"),
				func(namespace, name string, patchType types2.PatchType, data []byte) (runtime.Object, error) {
					return nil, apply.ErrReplace
				},
			),
		"Synced",
		"sync-repo",
		h.generate,
		nil,
	)
}

type Handler struct {
	ctx     context.Context
	gitjobs v1controller.GitJobController
	batch   batchv1controller.JobClient
	secrets corev1controller.SecretCache
	image   string
}

func (h Handler) generate(obj *v1.GitJob, status v1.GitJobStatus) ([]runtime.Object, v1.GitJobStatus, error) {
	// re-enqueue after syncInterval(seconds)
	interval := obj.Spec.SyncInterval
	if interval == 0 {
		interval = 15
	}

	if obj.Spec.Git.Revision == "" {
		if shouldSync(status, interval) {
			commit, err := git.LatestCommit(obj, h.secrets)
			if err != nil {
				logrus.Errorf("Error fetching latest commit: %v", err)
				kstatus.SetError(obj, err.Error())
				h.enqueueGitJob(obj, interval)
				return nil, obj.Status, nil
			} else if !kstatus.Stalled.IsTrue(obj) {
				kstatus.SetActive(obj)
				status = obj.Status
			}
			status.Commit = commit
			status.LastSyncedTime = metav1.Now()
		}
	} else {
		status.Commit = obj.Spec.Git.Revision
	}

	if obj.Status.Commit == "" {
		h.enqueueGitJob(obj, interval)
		return nil, status, nil
	}

	var result []runtime.Object

	if obj.Spec.Git.Credential.CABundle != nil {
		result = append(result, h.generateSecret(obj))
	}

	background := metav1.DeletePropagationBackground
	// if force delete is set, delete the job to make sure a new job is created
	if obj.Spec.ForceUpdateGeneration != status.UpdateGeneration {
		status.UpdateGeneration = obj.Spec.ForceUpdateGeneration
		logrus.Infof("Force update is requested for gitjob %s/%s, deleting job", obj.Namespace, obj.Name)
		if err := h.batch.Delete(obj.Namespace, jobName(obj), &metav1.DeleteOptions{PropagationPolicy: &background}); err != nil && !errors.IsNotFound(err) {
			return nil, status, err
		}
	}

	// if the job failed, e.g. because a helm registry was unreachable, delete the old job
	// only retry for failed jobs, job output has a log level so check for that
	if isJobError(obj) && strings.Contains(kstatus.Stalled.GetMessage(obj), "level=fatal") {
		logrus.Infof("Deleting failed job to trigger retry %s/%s due to: %s", obj.Namespace, jobName(obj), kstatus.Stalled.GetMessage(obj))
		if err := h.batch.Delete(obj.Namespace, jobName(obj), &metav1.DeleteOptions{PropagationPolicy: &background}); err != nil && !errors.IsNotFound(err) {
			return nil, status, fmt.Errorf("cannot delete failed job %s/%s: %v", obj.Namespace, jobName(obj), err)
		}
	}

	job, err := h.generateJob(obj)
	if err != nil {
		return nil, status, err
	}

	h.enqueueGitJob(obj, interval)
	status.ObservedGeneration = obj.Generation
	return append(result, job), status, nil
}

// isJobError returns true if the conditions from kstatus.SetError, used by job controller, are matched
func isJobError(obj *v1.GitJob) bool {
	return kstatus.Reconciling.IsFalse(obj) && kstatus.Stalled.IsTrue(obj) && kstatus.Stalled.GetReason(obj) == string(kstatus.Stalled) && kstatus.Stalled.GetMessage(obj) != ""
}

func (h Handler) enqueueGitJob(obj *v1.GitJob, interval int) {
	logrus.Debugf("Enqueueing gitjob %s/%s in %d seconds", obj.Namespace, obj.Name, interval)
	h.gitjobs.EnqueueAfter(obj.Namespace, obj.Name, time.Duration(interval)*time.Second)
}

func shouldSync(status v1.GitJobStatus, interval int) bool {
	return time.Now().Sub(status.LastSyncedTime.Time).Seconds() > float64(interval)
}

func (h Handler) generateSecret(obj *v1.GitJob) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: obj.Namespace,
			Name:      caBundleName(obj),
		},
		Data: map[string][]byte{
			bundleCAFile: obj.Spec.Git.CABundle,
		},
	}
}

func jobName(obj *v1.GitJob) string {
	return name.SafeConcatName(obj.Name, name.Hex(obj.Spec.Git.Repo+obj.Status.Commit, 5))
}

func caBundleName(obj *v1.GitJob) string {
	return fmt.Sprintf("%s-cabundle", obj.Name)
}

func (h Handler) generateJob(obj *v1.GitJob) (*batchv1.Job, error) {
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

	initContainer, err := h.generateInitContainer(obj)
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
	}

	return job, nil
}

func (h Handler) generateInitContainer(obj *v1.GitJob) (corev1.Container, error) {
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
		secret, err := h.secrets.Get(obj.Namespace, obj.Spec.Git.ClientSecretName)
		if err != nil {
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
		Image:        h.image,
		Name:         "gitcloner-initializer",
		VolumeMounts: volumeMounts,
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
