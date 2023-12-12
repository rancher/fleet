//go:generate mockgen --build_flags=--mod=mod -destination=../mocks/poller_mock.go -package=mocks github.com/rancher/gitjob/pkg/controller GitPoller
//go:generate mockgen --build_flags=--mod=mod -destination=../mocks/client_mock.go -package=mocks sigs.k8s.io/controller-runtime/pkg/client Client,SubResourceWriter

package controller

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/golang/mock/gomock"
	gitjobv1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	"github.com/rancher/gitjob/pkg/mocks"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestReconcile_AddOrModifyGitRepoWatchIsCalled_WhenGitRepoIsCreatedOrModified(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(gitjobv1.AddToScheme(scheme))
	utilruntime.Must(batchv1.AddToScheme(scheme))
	gitJob := gitjobv1.GitJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gitjob",
			Namespace: "default",
		},
	}
	namespacedName := types.NamespacedName{Name: gitJob.Name, Namespace: gitJob.Namespace}
	ctx := context.TODO()
	client := mocks.NewMockClient(mockCtrl)
	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	statusClient.EXPECT().Update(ctx, gomock.Any())
	client.EXPECT().Get(ctx, gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	client.EXPECT().Status().Return(statusClient)
	poller := mocks.NewMockGitPoller(mockCtrl)
	poller.EXPECT().AddOrModifyGitRepoWatch(ctx, gomock.Any()).Times(1)
	poller.EXPECT().CleanUpWatches(ctx).Times(0)

	r := GitJobReconciler{
		Client:    client,
		Scheme:    scheme,
		Image:     "",
		GitPoller: poller,
	}
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
}

func TestReconcile_PurgeWatchesIsCalled_WhenGitRepoIsCreatedOrModified(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(gitjobv1.AddToScheme(scheme))
	utilruntime.Must(batchv1.AddToScheme(scheme))
	ctx := context.TODO()
	namespacedName := types.NamespacedName{Name: "gitJob", Namespace: "default"}
	client := mocks.NewMockClient(mockCtrl)
	client.EXPECT().Get(ctx, namespacedName, gomock.Any()).Times(1).Return(errors.NewNotFound(schema.GroupResource{}, "NotFound"))
	poller := mocks.NewMockGitPoller(mockCtrl)
	poller.EXPECT().AddOrModifyGitRepoWatch(ctx, gomock.Any()).Times(0)
	poller.EXPECT().CleanUpWatches(ctx).Times(1)

	r := GitJobReconciler{
		Client:    client,
		Scheme:    scheme,
		Image:     "",
		GitPoller: poller,
	}
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
}

func TestNewJob(t *testing.T) {
	securityContext := &corev1.SecurityContext{
		AllowPrivilegeEscalation: &[]bool{false}[0],
		ReadOnlyRootFilesystem:   &[]bool{true}[0],
		Privileged:               &[]bool{false}[0],
		Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		RunAsNonRoot:             &[]bool{true}[0],
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(gitjobv1.AddToScheme(scheme))
	utilruntime.Must(batchv1.AddToScheme(scheme))
	ctx := context.TODO()
	poller := mocks.NewMockGitPoller(mockCtrl)
	poller.EXPECT().AddOrModifyGitRepoWatch(ctx, gomock.Any()).AnyTimes()
	poller.EXPECT().CleanUpWatches(ctx).AnyTimes()

	tests := map[string]struct {
		gitjob                 *gitjobv1.GitJob
		client                 client.Client
		expectedInitContainers []corev1.Container
		expectedVolumes        []corev1.Volume
		expectedErr            error
	}{
		"simple (no credentials, no ca, no skip tls)": {
			gitjob: &gitjobv1.GitJob{
				Spec: gitjobv1.GitJobSpec{Git: gitjobv1.GitInfo{Repo: "repo"}},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"gitcloner",
					},
					Args:  []string{"repo", "/workspace"},
					Image: "test",
					Name:  "gitcloner-initializer",
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      gitClonerVolumeName,
							MountPath: "/workspace",
						},
						{
							Name:      emptyDirVolumeName,
							MountPath: "/tmp",
						},
					},
					SecurityContext: securityContext,
				},
			},
			expectedVolumes: []corev1.Volume{
				{
					Name: gitClonerVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: emptyDirVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
			client: fake.NewFakeClient(),
		},
		"http credentials": {
			gitjob: &gitjobv1.GitJob{
				Spec: gitjobv1.GitJobSpec{
					Git: gitjobv1.GitInfo{
						Repo: "repo",
						Credential: gitjobv1.Credential{
							ClientSecretName: "secretName",
						},
					},
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"gitcloner",
					},
					Args:  []string{"repo", "/workspace", "--username", "user", "--password-file", "/gitjob/credentials/" + corev1.BasicAuthPasswordKey},
					Image: "test",
					Name:  "gitcloner-initializer",
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      gitClonerVolumeName,
							MountPath: "/workspace",
						},
						{
							Name:      emptyDirVolumeName,
							MountPath: "/tmp",
						},
						{
							Name:      gitCredentialVolumeName,
							MountPath: "/gitjob/credentials",
						},
					},
					SecurityContext: securityContext,
				},
			},
			expectedVolumes: []corev1.Volume{
				{
					Name: gitClonerVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: emptyDirVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: gitCredentialVolumeName,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "secretName",
						},
					},
				},
			},
			client: httpSecretMock(),
		},
		"ssh credentials": {
			gitjob: &gitjobv1.GitJob{
				Spec: gitjobv1.GitJobSpec{
					Git: gitjobv1.GitInfo{
						Repo: "repo",
						Credential: gitjobv1.Credential{
							ClientSecretName: "secretName",
						},
					},
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"gitcloner",
					},
					Args:  []string{"repo", "/workspace", "--ssh-private-key-file", "/gitjob/ssh/" + corev1.SSHAuthPrivateKey},
					Image: "test",
					Name:  "gitcloner-initializer",
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      gitClonerVolumeName,
							MountPath: "/workspace",
						},
						{
							Name:      emptyDirVolumeName,
							MountPath: "/tmp",
						},
						{
							Name:      gitCredentialVolumeName,
							MountPath: "/gitjob/ssh",
						},
					},
					SecurityContext: securityContext,
				},
			},
			expectedVolumes: []corev1.Volume{
				{
					Name: gitClonerVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: emptyDirVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: gitCredentialVolumeName,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "secretName",
						},
					},
				},
			},
			client: sshSecretMock(),
		},
		"custom CA": {
			gitjob: &gitjobv1.GitJob{
				Spec: gitjobv1.GitJobSpec{
					Git: gitjobv1.GitInfo{
						Credential: gitjobv1.Credential{
							CABundle: []byte("ca"),
						},
						Repo: "repo",
					},
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"gitcloner",
					},
					Args:  []string{"repo", "/workspace", "--ca-bundle-file", "/gitjob/cabundle/" + bundleCAFile},
					Image: "test",
					Name:  "gitcloner-initializer",
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      gitClonerVolumeName,
							MountPath: "/workspace",
						},
						{
							Name:      emptyDirVolumeName,
							MountPath: "/tmp",
						},
						{
							Name:      bundleCAVolumeName,
							MountPath: "/gitjob/cabundle",
						},
					},
					SecurityContext: securityContext,
				},
			},
			expectedVolumes: []corev1.Volume{
				{
					Name: gitClonerVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: emptyDirVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: bundleCAVolumeName,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "-cabundle",
						},
					},
				},
			},
		},
		"skip tls": {
			gitjob: &gitjobv1.GitJob{
				Spec: gitjobv1.GitJobSpec{
					Git: gitjobv1.GitInfo{
						Credential: gitjobv1.Credential{
							InsecureSkipTLSverify: true,
						},
						Repo: "repo",
					},
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"gitcloner",
					},
					Args:  []string{"repo", "/workspace", "--insecure-skip-tls"},
					Image: "test",
					Name:  "gitcloner-initializer",
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      gitClonerVolumeName,
							MountPath: "/workspace",
						},
						{
							Name:      emptyDirVolumeName,
							MountPath: "/tmp",
						},
					},
					SecurityContext: securityContext,
				},
			},
			expectedVolumes: []corev1.Volume{
				{
					Name: gitClonerVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: emptyDirVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			r := GitJobReconciler{
				Client:    test.client,
				Scheme:    scheme,
				Image:     "test",
				GitPoller: poller,
			}
			job, err := r.newJob(ctx, test.gitjob)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !cmp.Equal(job.Spec.Template.Spec.InitContainers, test.expectedInitContainers) {
				t.Fatalf("expected initContainers: %v, got: %v", test.expectedInitContainers, job.Spec.Template.Spec.InitContainers)
			}
			if !cmp.Equal(job.Spec.Template.Spec.Volumes, test.expectedVolumes) {
				t.Fatalf("expected volumes: %v, got: %v", test.expectedVolumes, job.Spec.Template.Spec.Volumes)
			}
		})
	}
}

func httpSecretMock() client.Client {
	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))

	return fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "secretName"},
		Data: map[string][]byte{
			corev1.BasicAuthUsernameKey: []byte("user"),
			corev1.BasicAuthPasswordKey: []byte("pass"),
		},
		Type: corev1.SecretTypeBasicAuth,
	}).Build()
}

func sshSecretMock() client.Client {
	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))

	return fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "secretName"},
		Data: map[string][]byte{
			corev1.SSHAuthPrivateKey: []byte("ssh key"),
		},
		Type: corev1.SecretTypeSSHAuth,
	}).Build()
}
