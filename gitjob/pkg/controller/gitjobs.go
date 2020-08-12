package controller

import (
	"context"
	"fmt"
	"time"

	v1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	v1controller "github.com/rancher/gitjob/pkg/generated/controllers/gitjob.cattle.io/v1"
	"github.com/rancher/gitjob/pkg/provider"
	"github.com/rancher/gitjob/pkg/provider/polling"
	"github.com/rancher/gitjob/pkg/types"
	"github.com/rancher/wrangler/pkg/apply"
	corev1controller "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/name"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	types2 "k8s.io/apimachinery/pkg/types"
)

var (
	image = map[string]string{
		"creds-init": "gcr.io/tekton-releases/github.com/tektoncd/pipeline/cmd/creds-init:v0.12.1",
		"git-init":   "gcr.io/tekton-releases/github.com/tektoncd/pipeline/cmd/git-init:v0.12.1",
		"entrypoint": "gcr.io/tekton-releases/github.com/tektoncd/pipeline/cmd/entrypoint:v0.12.1",
	}
)

func Register(ctx context.Context, cont *types.Context) {
	h := Handler{
		ctx: ctx,
		providers: []provider.Provider{
			polling.NewPolling(cont.Core.Core().V1().Secret().Cache()),
		},
	}

	v1controller.RegisterGitJobGeneratingHandler(
		ctx,
		cont.Gitjob.Gitjob().V1().GitJob(),
		cont.Apply.WithNoDelete().WithCacheTypes(cont.Batch.Batch().V1().Job()).WithPatcher(
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
	ctx       context.Context
	gitjobs   v1controller.GitJobController
	providers []provider.Provider
	secrets   corev1controller.SecretCache
}

func (h Handler) generate(obj *v1.GitJob, status v1.GitjobStatus) ([]runtime.Object, v1.GitjobStatus, error) {
	if obj.Spec.Git.Revision == "" {
		for _, provider := range h.providers {
			if provider.Supports(obj) {
				handledStatus, err := provider.Handle(h.ctx, obj)
				if err != nil {
					return nil, status, err
				}
				status = handledStatus
			}
		}
	} else {
		obj.Status.Commit = obj.Spec.Git.Revision
	}

	if obj.Status.Commit == "" {
		return nil, status, nil
	}

	var result []runtime.Object

	if obj.Spec.Git.Credential.CABundle != nil {
		result = append(result, h.generateConfigmap(obj))
	}

	job, err := h.generateJob(obj)
	if err != nil {
		return nil, status, err
	}

	// re-enqueue after syncInterval(seconds)
	interval := obj.Spec.SyncInterval
	if interval == 0 {
		interval = 15
	}
	h.gitjobs.EnqueueAfter(obj.Namespace, obj.Name, time.Duration(interval)*time.Second)

	return append(result, job), status, nil
}

func (h Handler) generateConfigmap(obj *v1.GitJob) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: obj.Namespace,
			Name:      caBundleName(obj),
		},
		BinaryData: map[string][]byte{
			"ca.crt": obj.Spec.Git.Credential.CABundle,
		},
	}
}

func caBundleName(obj *v1.GitJob) string {
	return fmt.Sprintf("%s-CABundle", obj.Name)
}

func (h Handler) generateJob(obj *v1.GitJob) (*batchv1.Job, error) {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: obj.Namespace,
			Name:      name.SafeConcatName(obj.Name, name.Hex(obj.Spec.Git.Repo+obj.Status.Commit, 5)),
		},
		Spec: obj.Spec.JobSpec,
	}

	if obj.Status.GithubMeta != nil && obj.Status.GithubMeta.Event != "" {
		job.Annotations = map[string]string{
			"event": obj.Status.GithubMeta.Event,
		}
	}

	cloneContainer := h.generateCloneContainer(obj)
	initContainers, err := h.generateInitContainer(obj)
	if err != nil {
		return nil, err
	}

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
	)

	//setup custom ca
	if obj.Spec.Git.CABundle != nil {
		job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes, corev1.Volume{
			Name: "custom-ca",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: caBundleName(obj),
					},
				},
			},
		})
	}

	if obj.Spec.Git.GitSecretName != "" {
		job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "git-credential",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: obj.Spec.Git.GitSecretName,
					},
				},
			},
		)
	}

	for i := range job.Spec.Template.Spec.Containers {
		job.Spec.Template.Spec.Containers[i].Args = append([]string{
			"-wait_file",
			"/tekton/tools/0",
			"-post_file",
			"/tekton/tools/1",
			"-termination_path",
			"/tekton/termination",
			"-entrypoint",
		}, append(job.Spec.Template.Spec.Containers[i].Command, job.Spec.Template.Spec.Containers[i].Args...)...)
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
		job.Spec.Template.Spec.Containers[i].TerminationMessagePath = "/tekton/termination"
		job.Spec.Template.Spec.Containers[i].TerminationMessagePolicy = corev1.TerminationMessageReadFile
	}

	job.Spec.Template.Spec.Containers = append([]corev1.Container{cloneContainer}, job.Spec.Template.Spec.Containers...)
	return job, nil
}

func (h Handler) generateCloneContainer(obj *v1.GitJob) corev1.Container {
	c := corev1.Container{
		Image: image["git-init"],
		Name:  "step-git-source",
		Args: []string{
			"-post_file",
			"/tekton/tools/0",
			"-termination_path",
			"/tekton/termination",
			"-entrypoint",
			"/ko-app/git-init",
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
		Env: []corev1.EnvVar{
			{
				Name:  "HOME",
				Value: "/tekton/home",
			},
		},
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
		},
		TerminationMessagePath:   "/tekton/termination",
		TerminationMessagePolicy: corev1.TerminationMessageReadFile,
	}

	// setup ssl verify
	if obj.Spec.Git.InsecureSkipTLSverify {
		c.Args = append(c.Args, "-sslVerify", "true")
	}

	// setup CA bundle
	if obj.Spec.Git.CABundle != nil {
		c.Env = append(c.Env, corev1.EnvVar{
			Name:  "GIT_SSL_CAINFO",
			Value: "/ssl/ca.crt",
		})

		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      "custom-ca",
			MountPath: "/ssl/ca.crt",
		})
	}

	return c
}

func (h Handler) generateInitContainer(obj *v1.GitJob) ([]corev1.Container, error) {
	initContainers := []corev1.Container{
		{
			Command: []string{
				"sh",
			},
			Args: []string{
				"-c",
				"mkdir -p /workspace/source",
			},
			Image: "busybox",
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
				"/ko-app/entrypoint",
				"/tekton/tools/entrypoint",
			},
			Image: image["entrypoint"],
			Name:  "place-tools",
			VolumeMounts: []corev1.VolumeMount{
				{
					MountPath: "/tekton/tools",
					Name:      "tekton-internal-tools",
				},
			},
		},
	}
	if obj.Spec.Git.GitSecretName != "" {
		secretType, err := h.inspectSecretType(obj.Spec.Git.GitSecretName, obj.Namespace)
		if err != nil {
			return nil, err
		}
		initContainers = append([]corev1.Container{
			{
				Args: []string{
					fmt.Sprintf("-%s-git=%s=%s", secretType, obj.Spec.Git.GitSecretName, obj.Spec.Git.GitHostname),
				},
				Name: "creds-init",
				Command: []string{
					"/ko-app/creds-init",
				},
				Env: []corev1.EnvVar{
					{
						Name:  "HOME",
						Value: "/tekton/home",
					},
				},
				Image: image["creds-init"],
				VolumeMounts: []corev1.VolumeMount{
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
					{
						MountPath: fmt.Sprintf("/tekton/creds-secrets/%s", obj.Spec.Git.GitSecretName),
						Name:      "git-credential",
					},
				},
			},
		}, initContainers...)
	}
	return initContainers, nil
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
