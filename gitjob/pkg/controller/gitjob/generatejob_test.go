package gitjob

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/rancher/gitjob/internal/mocks"
	v1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	corev1controller "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGenerateJob(t *testing.T) {
	ctrl := gomock.NewController(t)

	tests := map[string]struct {
		gitjob                 *v1.GitJob
		secret                 corev1controller.SecretCache
		expectedInitContainers []corev1.Container
		expectedVolumes        []corev1.Volume
		expectedErr            error
	}{
		"simple (no credentials, no ca, no skip tls)": {
			gitjob: &v1.GitJob{
				Spec: v1.GitJobSpec{Git: v1.GitInfo{Repo: "repo"}},
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
					},
				},
			},
			expectedVolumes: []corev1.Volume{
				{
					Name: gitClonerVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
		},
		"http credentials": {
			gitjob: &v1.GitJob{
				Spec: v1.GitJobSpec{
					Git: v1.GitInfo{
						Repo: "repo",
						Credential: v1.Credential{
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
							Name:      gitCredentialVolumeName,
							MountPath: "/gitjob/credentials",
						},
					},
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
					Name: gitCredentialVolumeName,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "secretName",
						},
					},
				},
			},
			secret: httpSecretMock(ctrl),
		},
		"ssh credentials": {
			gitjob: &v1.GitJob{
				Spec: v1.GitJobSpec{
					Git: v1.GitInfo{
						Repo: "repo",
						Credential: v1.Credential{
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
							Name:      gitCredentialVolumeName,
							MountPath: "/gitjob/ssh",
						},
					},
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
					Name: gitCredentialVolumeName,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "secretName",
						},
					},
				},
			},
			secret: sshSecretMock(ctrl),
		},
		"custom CA": {
			gitjob: &v1.GitJob{
				Spec: v1.GitJobSpec{
					Git: v1.GitInfo{
						Credential: v1.Credential{
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
							Name:      bundleCAVolumeName,
							MountPath: "/gitjob/cabundle",
						},
					},
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
			gitjob: &v1.GitJob{
				Spec: v1.GitJobSpec{
					Git: v1.GitInfo{
						Credential: v1.Credential{
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
					},
				},
			},
			expectedVolumes: []corev1.Volume{
				{
					Name: gitClonerVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
		},
	}

	for _, test := range tests {
		h := Handler{
			image:   "test",
			secrets: test.secret,
		}
		job, err := h.generateJob(test.gitjob)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !cmp.Equal(job.Spec.Template.Spec.InitContainers, test.expectedInitContainers) {
			t.Fatalf("expected initContainers: %v, got: %v", test.expectedInitContainers, job.Spec.Template.Spec.InitContainers)
		}
		if !cmp.Equal(job.Spec.Template.Spec.Volumes, test.expectedVolumes) {
			t.Fatalf("expected volumes: %v, got: %v", test.expectedVolumes, job.Spec.Template.Spec.Volumes)
		}
	}
}

func httpSecretMock(ctrl *gomock.Controller) corev1controller.SecretCache {
	secretmock := mocks.NewMockSecretCache(ctrl)
	secretmock.EXPECT().Get(gomock.Any(), gomock.Any()).Return(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{},
		Data: map[string][]byte{
			corev1.BasicAuthUsernameKey: []byte("user"),
			corev1.BasicAuthPasswordKey: []byte("pass"),
		},
		Type: corev1.SecretTypeBasicAuth,
	}, nil)

	return secretmock
}

func sshSecretMock(ctrl *gomock.Controller) corev1controller.SecretCache {
	secretmock := mocks.NewMockSecretCache(ctrl)
	secretmock.EXPECT().Get(gomock.Any(), gomock.Any()).Return(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{},
		Data: map[string][]byte{
			corev1.SSHAuthPrivateKey: []byte("ssh key"),
		},
		Type: corev1.SecretTypeSSHAuth,
	}, nil)

	return secretmock
}
