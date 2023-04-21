package gitjob

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	v1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	v1controller "github.com/rancher/gitjob/pkg/generated/controllers/gitjob.cattle.io/v1"
	"github.com/rancher/gitjob/pkg/git"
	"github.com/rancher/gitjob/pkg/types"
	"github.com/rancher/wrangler/pkg/apply"
	batchv1controller "github.com/rancher/wrangler/pkg/generated/controllers/batch/v1"
	corev1controller "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/kstatus"
	"github.com/rancher/wrangler/pkg/name"
	giturls "github.com/whilp/git-urls"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	types2 "k8s.io/apimachinery/pkg/types"
)

const (
	bundleCAVolumeName = "additional-ca"
	bundleCAFile       = "additional-ca.crt"
	bundleDir          = "/etc/rancher/ssl"
)

func Register(ctx context.Context, cont *types.Context) {
	h := Handler{
		ctx:     ctx,
		gitjobs: cont.Gitjob.Gitjob().V1().GitJob(),
		secrets: cont.Core.Core().V1().Secret().Cache(),
		batch:   cont.Batch.Batch().V1().Job(),
		Image:   cont.Image,
	}

	v1controller.RegisterGitJobGeneratingHandler(
		ctx,
		cont.Gitjob.Gitjob().V1().GitJob(),
		cont.Apply.WithSetOwnerReference(true, false).WithCacheTypes(cont.Batch.Batch().V1().Job(), cont.Core.Core().V1().Secret()).WithPatcher(
			batchv1.SchemeGroupVersion.WithKind("Job"),
			func(namespace, name string, patchType types2.PatchType, data []byte) (runtime.Object, error) {
				return nil, apply.ErrReplace
			}),
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
	Image   string
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
				kstatus.SetError(obj, err.Error())
				h.gitjobs.EnqueueAfter(obj.Namespace, obj.Name, time.Duration(interval)*time.Second)
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
		h.gitjobs.EnqueueAfter(obj.Namespace, obj.Name, time.Duration(interval)*time.Second)
		return nil, status, nil
	}

	var result []runtime.Object

	if obj.Spec.Git.Credential.CABundle != nil {
		result = append(result, h.generateSecret(obj))
	}

	// if force delete is set, delete the job to make sure a new job is created
	if obj.Spec.ForceUpdateGeneration != status.UpdateGeneration {
		status.UpdateGeneration = obj.Spec.ForceUpdateGeneration
		if err := h.gitjobs.Delete(obj.Namespace, jobName(obj), &metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
			return nil, status, err
		}
	}

	job, err := h.generateJob(obj)
	if err != nil {
		return nil, status, err
	}

	h.gitjobs.EnqueueAfter(obj.Namespace, obj.Name, time.Duration(interval)*time.Second)
	status.ObservedGeneration = obj.Generation
	return append(result, job), status, nil
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

	cloneContainer, err := h.generateCloneContainer(obj)
	if err != nil {
		return nil, err
	}
	initContainers := h.generateInitContainer()

	job.Spec.Template.Spec.InitContainers = initContainers
	job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes,
		corev1.Volume{
			Name: "workspace",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		corev1.Volume{
			Name: "tekton-internal-workspace",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		corev1.Volume{
			Name: "tekton-internal-home",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		corev1.Volume{
			Name: "tekton-internal-tools",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		corev1.Volume{
			Name: "tekton-internal-results",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		corev1.Volume{
			Name: "tekton-creds",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	)

	//setup custom ca
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
				Name: "git-credential",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: obj.Spec.Git.ClientSecretName,
					},
				},
			},
		)
	}

	for i := range job.Spec.Template.Spec.Containers {
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

		for _, envVar := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
			if val, ok := os.LookupEnv(envVar); ok {
				job.Spec.Template.Spec.Containers[i].Env = append(job.Spec.Template.Spec.Containers[i].Env, corev1.EnvVar{Name: envVar, Value: val})
			}
		}

		cArgs := append([]string{"--"}, job.Spec.Template.Spec.Containers[i].Args...)
		job.Spec.Template.Spec.Containers[i].Args = append([]string{
			"-wait_file",
			"/tekton/tools/0",
			"-post_file",
			"/tekton/tools/1",
			"-termination_path",
			"/tekton/tools/termination_path",
			"-entrypoint",
		}, append(job.Spec.Template.Spec.Containers[i].Command, cArgs...)...)
		job.Spec.Template.Spec.Containers[i].Command = []string{"/tekton/tools/entrypoint"}
		job.Spec.Template.Spec.Containers[i].VolumeMounts = append(job.Spec.Template.Spec.Containers[i].VolumeMounts, []corev1.VolumeMount{
			{
				MountPath: "/tekton/tools",
				Name:      "tekton-internal-tools",
			},
			{
				MountPath: "/workspace",
				Name:      "tekton-internal-workspace",
			},
			{
				MountPath: "/tekton/home",
				Name:      "tekton-internal-home",
			},
			{
				MountPath: "/tekton/results",
				Name:      "tekton-internal-results",
			},
		}...)
	}

	job.Spec.Template.Spec.Containers = append([]corev1.Container{cloneContainer}, job.Spec.Template.Spec.Containers...)
	return job, nil
}

func (h Handler) generateCloneContainer(obj *v1.GitJob) (corev1.Container, error) {
	var env []corev1.EnvVar

	for _, envVar := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"} {
		if val, ok := os.LookupEnv(envVar); ok {
			env = append(env, corev1.EnvVar{Name: envVar, Value: val})
		}
	}

	c := corev1.Container{
		Image: h.Image,
		Name:  "step-git-source",
		Args: []string{
			"-post_file",
			"/tekton/tools/0",
			"-termination_path",
			"/tekton/termination",
			"-entrypoint",
			"/usr/bin/git-init",
			"--",
			"-url",
			obj.Spec.Git.Repo,
			"-revision",
			obj.Status.Commit,
			"-path",
			"/workspace/source",
		},
		Command: []string{
			"/tekton/tools/entrypoint",
		},
		Env:        env,
		WorkingDir: "/workspace",
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "tekton-internal-workspace",
				MountPath: "/workspace",
			},
			{
				Name:      "tekton-internal-tools",
				MountPath: "/tekton/tools",
			},
			{
				Name:      "tekton-internal-home",
				MountPath: "/tekton/home",
			},
			{
				Name:      "tekton-creds",
				MountPath: "/tekton/creds",
			},
		},
		TerminationMessagePath:   "/tekton/termination",
		TerminationMessagePolicy: corev1.TerminationMessageReadFile,
	}

	if obj.Spec.Git.ClientSecretName != "" {
		hostname, err := parseHostname(obj.Spec.Git.Repo)
		if err != nil {
			return corev1.Container{}, err
		}

		secretType, err := h.inspectSecretType(obj.Spec.Git.ClientSecretName, obj.Namespace)
		if err != nil {
			return corev1.Container{}, err
		}

		//tekton requires https:// to be prefixed on hostname https://github.com/tektoncd/pipeline/issues/2409
		if secretType == "basic" {
			hostname = "https://" + hostname
		}

		c.Args = append([]string{fmt.Sprintf("-%s-git=%s=%s", secretType, obj.Spec.Git.ClientSecretName, hostname)}, c.Args...)
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			MountPath: fmt.Sprintf("/tekton/creds-secrets/%s", obj.Spec.Git.ClientSecretName),
			Name:      "git-credential",
		})
	}

	// setup ssl verify
	if obj.Spec.Git.InsecureSkipTLSverify {
		c.Args = append(c.Args, "-sslVerify=false")
	}

	// setup CA bundle
	if obj.Spec.Git.CABundle != nil {
		c.Env = append(c.Env, corev1.EnvVar{
			Name:  "GIT_SSL_CAINFO",
			Value: filepath.Join(bundleDir, bundleCAFile),
		})

		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      bundleCAVolumeName,
			MountPath: bundleDir,
		})
	}

	return c, nil
}

func (h Handler) generateInitContainer() []corev1.Container {
	initContainers := []corev1.Container{
		{
			Command: []string{
				"sh",
			},
			Args: []string{
				"-c",
				"mkdir -p /workspace/source",
			},
			Image: h.Image,
			Name:  "working-dir-initializer",
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "tekton-internal-workspace",
					MountPath: "/workspace",
				},
				{
					Name:      "tekton-internal-home",
					MountPath: "/tekton/home",
				},
				{
					Name:      "tekton-internal-results",
					MountPath: "/tekton/results",
				},
			},
		},
		{
			Command: []string{
				"cp",
				"/usr/bin/entrypoint",
				"/tekton/tools/entrypoint",
			},
			Image: h.Image,
			Name:  "place-tools",
			VolumeMounts: []corev1.VolumeMount{
				{
					MountPath: "/tekton/tools",
					Name:      "tekton-internal-tools",
				},
			},
		},
	}

	return initContainers
}

func (h Handler) inspectSecretType(secretName, namespace string) (string, error) {
	secret, err := h.secrets.Get(namespace, secretName)
	if err != nil {
		return "", err
	}

	if secret.Type == corev1.SecretTypeBasicAuth {
		return "basic", nil
	} else if secret.Type == corev1.SecretTypeSSHAuth {
		return "ssh", nil
	}

	return "", fmt.Errorf("git secret can only be ssh or basic auth, type is %v", secret.Type)
}

func parseHostname(repo string) (string, error) {
	u, err := giturls.Parse(repo)
	if err != nil {
		return "", err
	}

	return u.Host, nil
}
