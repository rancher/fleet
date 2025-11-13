//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/client_mock.go -package=mocks sigs.k8s.io/controller-runtime/pkg/client Client,SubResourceWriter

package reconciler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	fleetapply "github.com/rancher/fleet/internal/cmd/cli/apply"
	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/mocks"
	"github.com/rancher/fleet/internal/ssh"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	"go.uber.org/mock/gomock"

	fleetevent "github.com/rancher/fleet/pkg/event"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func getCondition(gitrepo *fleetv1.GitRepo, condType string) (genericcondition.GenericCondition, bool) {
	for _, cond := range gitrepo.Status.Conditions {
		if cond.Type == condType {
			return cond, true
		}
	}
	return genericcondition.GenericCondition{}, false
}

type ClockMock struct {
	t time.Time
}

func (m ClockMock) Now() time.Time                  { return m.t }
func (m ClockMock) Since(t time.Time) time.Duration { return m.t.Sub(t) }

// gitRepoMatcher implements a gomock matcher that checks for gitrepos.
// It only checks for the expected name and namespace so far
type gitRepoMatcher struct {
	gitrepo fleetv1.GitRepo
}

func (m gitRepoMatcher) Matches(x interface{}) bool {
	gitrepo, ok := x.(*fleetv1.GitRepo)
	if !ok {
		return false
	}
	return m.gitrepo.Name == gitrepo.Name && m.gitrepo.Namespace == gitrepo.Namespace
}

func (m gitRepoMatcher) String() string {
	return fmt.Sprintf("Gitrepo %s-%s", m.gitrepo.Name, m.gitrepo.Namespace)
}

type gitRepoPointerMatcher struct {
}

func (m gitRepoPointerMatcher) Matches(x interface{}) bool {
	_, ok := x.(*fleetv1.GitRepo)
	return ok
}

func (m gitRepoPointerMatcher) String() string {
	return ""
}

func TestReconcile_Error_WhenGitrepoRestrictionsAreNotMet(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(batchv1.AddToScheme(scheme))
	gitRepo := fleetv1.GitRepo{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gitrepo",
			Namespace: "default",
		},
	}
	namespacedName := types.NamespacedName{Name: gitRepo.Name, Namespace: gitRepo.Namespace}
	mockClient := mocks.NewMockK8sClient(mockCtrl)
	mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(ctx context.Context, restrictions *fleetv1.GitRepoRestrictionList, ns client.InNamespace) error {
			// fill the restrictions with a couple of allowed namespaces.
			// As the gitrepo has no target namespace restrictions won't be met
			restriction := fleetv1.GitRepoRestriction{AllowedTargetNamespaces: []string{"ns1", "ns2"}}
			restrictions.Items = append(restrictions.Items, restriction)
			return nil
		},
	)

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), &gitRepoPointerMatcher{}, gomock.Any()).Times(2).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, gitrepo *fleetv1.GitRepo, opts ...interface{}) error {
			gitrepo.Name = gitRepo.Name
			gitrepo.Namespace = gitRepo.Namespace
			return nil
		},
	)
	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	mockClient.EXPECT().Status().Times(1).Return(statusClient)
	statusClient.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, repo *fleetv1.GitRepo, opts ...interface{}) {
			if len(repo.Status.Conditions) == 0 {
				t.Errorf("expecting to have Conditions, got none")
			}
			if repo.Status.Conditions[0].Message != "empty targetNamespace denied, because allowedTargetNamespaces restriction is present" {
				t.Errorf("Expecting condition message [empty targetNamespace denied, because allowedTargetNamespaces restriction is present], got [%s]", repo.Status.Conditions[0].Message)
			}
		},
	)

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)
	recorderMock.EXPECT().Event(
		&gitRepoMatcher{gitRepo},
		fleetevent.Warning,
		"FailedToApplyRestrictions",
		"empty targetNamespace denied, because allowedTargetNamespaces restriction is present",
	)

	r := GitJobReconciler{
		Client:   mockClient,
		Scheme:   scheme,
		Image:    "",
		Clock:    RealClock{},
		Recorder: recorderMock,
	}

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err == nil {
		t.Errorf("expecting an error, got nil")
	}
	if err.Error() != "empty targetNamespace denied, because allowedTargetNamespaces restriction is present" {
		t.Errorf("unexpected error %v", err)
	}
}

func TestReconcile_Error_WhenGetGitJobErrors(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(batchv1.AddToScheme(scheme))
	gitRepo := fleetv1.GitRepo{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gitrepo",
			Namespace: "default",
		},
	}
	namespacedName := types.NamespacedName{Name: gitRepo.Name, Namespace: gitRepo.Namespace}
	mockClient := mocks.NewMockK8sClient(mockCtrl)
	mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&fleetv1.GitRepo{}), gomock.Any()).
		Times(3).
		DoAndReturn(
			func(ctx context.Context, req types.NamespacedName, gitrepo *fleetv1.GitRepo, opts ...interface{}) error {
				gitrepo.Name = gitRepo.Name
				gitrepo.Namespace = gitRepo.Namespace
				gitrepo.Spec.Repo = "repo"
				controllerutil.AddFinalizer(gitrepo, finalize.GitRepoFinalizer)
				gitrepo.Status.Commit = "dd45c7ad68e10307765104fea4a1f5997643020f"
				return nil
			},
		)

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, job *batchv1.Job, opts ...interface{}) error {
			return fmt.Errorf("GITJOB ERROR")
		},
	)

	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	mockClient.EXPECT().Status().Times(1).Return(statusClient)
	statusClient.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, repo *fleetv1.GitRepo, opts ...interface{}) {
			c, found := getCondition(repo, fleetv1.GitRepoAcceptedCondition)
			if !found {
				t.Errorf("expecting to find the %s condition and could not find it.", fleetv1.GitRepoAcceptedCondition)
			}
			if !strings.Contains(c.Message, "GITJOB ERROR") {
				t.Errorf("expecting message containing [GITJOB ERROR] in condition, got [%s]", c.Message)
			}
		},
	)

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)

	recorderMock.EXPECT().Event(
		&gitRepoMatcher{gitRepo},
		fleetevent.Warning,
		"FailedToGetGitJob",
		"error retrieving git job: GITJOB ERROR",
	)

	r := GitJobReconciler{
		Client:   mockClient,
		Scheme:   scheme,
		Image:    "",
		Clock:    RealClock{},
		Recorder: recorderMock,
	}

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err == nil {
		t.Errorf("expecting an error, got nil")
	}
	if err.Error() != "error retrieving git job: GITJOB ERROR" {
		t.Errorf("unexpected error %v", err)
	}
}

func TestReconcile_Error_WhenSecretDoesNotExist(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()
	scheme := runtime.NewScheme()
	utilruntime.Must(batchv1.AddToScheme(scheme))
	gitRepo := fleetv1.GitRepo{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gitrepo",
			Namespace: "default",
		},
	}
	namespacedName := types.NamespacedName{Name: gitRepo.Name, Namespace: gitRepo.Namespace}
	mockClient := mocks.NewMockK8sClient(mockCtrl)
	mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), &gitRepoPointerMatcher{}, gomock.Any()).Times(3).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, gitrepo *fleetv1.GitRepo, opts ...interface{}) error {
			gitrepo.Name = gitRepo.Name
			gitrepo.Namespace = gitRepo.Namespace
			gitrepo.Spec.Repo = "repo"
			gitrepo.Spec.HelmSecretNameForPaths = "somevalue"
			controllerutil.AddFinalizer(gitrepo, finalize.GitRepoFinalizer)
			gitrepo.Status.Commit = "dd45c7ad68e10307765104fea4a1f5997643020f"
			// use a different polling commit to force the creation of the gitjob
			gitrepo.Status.PollingCommit = "1883fd54bc5dfd225acf02aecbb6cb8020458e33"
			return nil
		},
	)

	// we need to return a NotFound error, so the code tries to create it.
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&batchv1.Job{}), gomock.Any()).
		Times(1).
		DoAndReturn(
			func(ctx context.Context, req types.NamespacedName, job *batchv1.Job, opts ...interface{}) error {
				return apierrors.NewNotFound(schema.GroupResource{}, "TEST ERROR")
			},
		).Times(2)

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, job *corev1.Secret, opts ...interface{}) error {
			return fmt.Errorf("SECRET ERROR")
		},
	)

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)

	recorderMock.EXPECT().Event(
		&gitRepoMatcher{gitRepo},
		fleetevent.Warning,
		"FailedValidatingSecret",
		"failed to look up HelmSecretNameForPaths, error: SECRET ERROR",
	)

	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	mockClient.EXPECT().Status().Times(1).Return(statusClient)
	statusClient.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, repo *fleetv1.GitRepo, opts ...interface{}) {
			c, found := getCondition(repo, fleetv1.GitRepoAcceptedCondition)
			if !found {
				t.Errorf("expecting to find the %s condition and could not find it.", fleetv1.GitRepoAcceptedCondition)
			}
			if c.Message != "error validating external secrets: failed to look up HelmSecretNameForPaths, error: SECRET ERROR" {
				t.Errorf("expecting message [failed to look up HelmSecretNameForPaths, error: SECRET ERROR] in condition, got [%s]", c.Message)
			}
		},
	)

	r := GitJobReconciler{
		Client:   mockClient,
		Scheme:   scheme,
		Image:    "",
		Clock:    RealClock{},
		Recorder: recorderMock,
	}

	ctx := context.TODO()
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err == nil {
		t.Errorf("expecting an error, got nil")
	}
	if err.Error() != "error validating external secrets: failed to look up HelmSecretNameForPaths, error: SECRET ERROR" {
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
	utilruntime.Must(batchv1.AddToScheme(scheme))
	utilruntime.Must(fleetv1.AddToScheme(scheme))
	ctx := context.TODO()

	// define the default tolerations that all jobs have
	defaultTolerations := []corev1.Toleration{
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
	}
	tests := map[string]struct {
		gitrepo                *fleetv1.GitRepo
		strictHostKeyChecks    bool
		clientObjects          []runtime.Object
		deploymentTolerations  []corev1.Toleration
		expectedInitContainers []corev1.Container
		expectedContainers     []corev1.Container
		expectedVolumes        []corev1.Volume
		expectedErr            error
	}{
		"simple (no credentials, no ca, no skip tls)": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{Repo: "repo"},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"log.sh",
					},
					Args: []string{
						"fleet",
						"gitcloner",
						"repo",
						"/workspace",
						"--branch",
						"master",
					},
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
					Env: []corev1.EnvVar{
						{
							Name:  fleetapply.JSONOutputEnvVar,
							Value: "true",
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
					Name: emptyDirVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
			clientObjects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "known-hosts",
						Namespace: "cattle-fleet-system",
					},
					Data: map[string]string{
						// Prevent deployment error about config map not existing, but the data
						// does not matter in this test case.
						"known_hosts": "",
					},
				},
			},
		},
		"simple with custom branch": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					Repo:   "repo",
					Branch: "foo",
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"log.sh",
					},
					Args: []string{
						"fleet",
						"gitcloner",
						"repo",
						"/workspace",
						"--branch",
						"foo",
					},
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
					Env: []corev1.EnvVar{
						{
							Name:  fleetapply.JSONOutputEnvVar,
							Value: "true",
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
					Name: emptyDirVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
			clientObjects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "known-hosts",
						Namespace: "cattle-fleet-system",
					},
					Data: map[string]string{
						// Prevent deployment error about config map not existing, but the data
						// does not matter in this test case.
						"known_hosts": "",
					},
				},
			},
		},
		"simple with custom revision": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					Repo:     "repo",
					Revision: "foo",
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"log.sh",
					},
					Args: []string{
						"fleet",
						"gitcloner",
						"repo",
						"/workspace",
						"--revision",
						"foo",
					},
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
					Env: []corev1.EnvVar{
						{
							Name:  fleetapply.JSONOutputEnvVar,
							Value: "true",
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
					Name: emptyDirVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
			clientObjects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "known-hosts",
						Namespace: "cattle-fleet-system",
					},
					Data: map[string]string{
						// Prevent deployment error about config map not existing, but the data
						// does not matter in this test case.
						"known_hosts": "",
					},
				},
			},
		},
		"http credentials": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					Repo:             "repo",
					ClientSecretName: "secretName",
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"log.sh",
					},
					Args: []string{
						"fleet",
						"gitcloner",
						"repo",
						"/workspace",
						"--branch",
						"master",
						"--username",
						"user",
						"--password-file",
						"/gitjob/credentials/" + corev1.BasicAuthPasswordKey,
					},
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
					Env: []corev1.EnvVar{
						{
							Name:  fleetapply.JSONOutputEnvVar,
							Value: "true",
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
			clientObjects: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "secretName",
						Namespace: "default",
					},
					Data: map[string][]byte{
						corev1.BasicAuthUsernameKey: []byte("user"),
						corev1.BasicAuthPasswordKey: []byte("pass"),
					},
					Type: corev1.SecretTypeBasicAuth,
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "known-hosts",
						Namespace: "cattle-fleet-system",
					},
					Data: map[string]string{
						// Prevent deployment error about config map not existing, but the data
						// does not matter in this test case.
						"known_hosts": "",
					},
				},
			},
		},
		"ssh credentials": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					Repo:             "repo",
					ClientSecretName: "secretName",
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"log.sh",
					},
					Args: []string{
						"fleet",
						"gitcloner",
						"repo",
						"/workspace",
						"--branch",
						"master",
						"--ssh-private-key-file",
						"/gitjob/ssh/" + corev1.SSHAuthPrivateKey,
					},
					// FLEET_KNOWN_HOSTS not expected here as strict host key checks are disabled
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
					Env: []corev1.EnvVar{
						{
							Name:  fleetapply.JSONOutputEnvVar,
							Value: "true",
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
			clientObjects: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "secretName"},
					Data: map[string][]byte{
						corev1.SSHAuthPrivateKey: []byte("ssh key"),
						"known_hosts":            []byte("foo"),
					},
					Type: corev1.SecretTypeSSHAuth,
				},
			},
		},
		"no ssh credentials, known_hosts info found in gitcredential secret": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					Repo: "ssh://repo",
				},
			},
			strictHostKeyChecks: true,
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"log.sh",
					},
					Args: []string{
						"fleet",
						"gitcloner",
						"ssh://repo",
						"/workspace",
						"--branch",
						"master",
						"--ssh-private-key-file",
						"/gitjob/ssh/" + corev1.SSHAuthPrivateKey,
					},
					Env: []corev1.EnvVar{
						{
							Name:  fleetapply.JSONOutputEnvVar,
							Value: "true",
						},
						{
							Name:  "FLEET_KNOWN_HOSTS",
							Value: "some known hosts",
						},
					},
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
							SecretName: "gitcredential",
						},
					},
				},
			},
			clientObjects: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "gitcredential",
					},
					Data: map[string][]byte{
						corev1.SSHAuthPrivateKey: []byte("ssh key"),
						"known_hosts":            []byte("some known hosts"),
					},
					Type: corev1.SecretTypeSSHAuth,
				},
			},
		},
		"ssh credentials, incomplete secret, known_hosts found in config map": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					Repo:             "ssh://repo",
					ClientSecretName: "secretName",
				},
			},
			strictHostKeyChecks: true,
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"log.sh",
					},
					Args: []string{
						"fleet",
						"gitcloner",
						"ssh://repo",
						"/workspace",
						"--branch",
						"master",
						"--ssh-private-key-file",
						"/gitjob/ssh/" + corev1.SSHAuthPrivateKey,
					},
					Env: []corev1.EnvVar{
						{
							Name:  fleetapply.JSONOutputEnvVar,
							Value: "true",
						},
						{
							Name:  "FLEET_KNOWN_HOSTS",
							Value: "foo",
						},
					},
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
			clientObjects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "known-hosts",
						Namespace: "cattle-fleet-system",
					},
					Data: map[string]string{
						"known_hosts": "foo",
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "secretName",
						Namespace: "default",
					},
					Data: map[string][]byte{
						corev1.SSHAuthPrivateKey: []byte("ssh key"),
					},
					Type: corev1.SecretTypeSSHAuth,
				},
			},
		},
		"ssh credentials, no secret, known_hosts found in config map": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					Repo: "ssh://repo",
				},
			},
			strictHostKeyChecks: true,
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"log.sh",
					},
					Args: []string{
						"fleet",
						"gitcloner",
						"ssh://repo",
						"/workspace",
						"--branch",
						"master",
					},
					Env: []corev1.EnvVar{
						{
							Name:  fleetapply.JSONOutputEnvVar,
							Value: "true",
						},
						{
							Name:  "FLEET_KNOWN_HOSTS",
							Value: "foo",
						},
					},
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
			clientObjects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "known-hosts",
						Namespace: "cattle-fleet-system",
					},
					Data: map[string]string{
						"known_hosts": "foo",
					},
				},
			},
		},
		"github app credentials": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					Repo:             "repo",
					ClientSecretName: "secretName",
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"log.sh",
					},
					Args: []string{
						"fleet",
						"gitcloner",
						"repo",
						"/workspace",
						"--branch",
						"master",
						"--github-app-id",
						"123",
						"--github-app-installation-id",
						"456",
						"--github-app-key-file",
						"/gitjob/githubapp/github_app_private_key",
					},
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
							MountPath: "/gitjob/githubapp",
						},
					},
					SecurityContext: securityContext,
					Env: []corev1.EnvVar{
						{
							Name:  fleetapply.JSONOutputEnvVar,
							Value: "true",
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
			clientObjects: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "secretName"},
					Data: map[string][]byte{
						"github_app_id":              []byte("123"),
						"github_app_installation_id": []byte("456"),
						"github_app_private_key":     []byte("private key"),
					},
					Type: corev1.SecretTypeOpaque,
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "known-hosts",
						Namespace: "cattle-fleet-system",
					},
					Data: map[string]string{
						// Prevent deployment error about config map not existing, but the data
						// does not matter in this test case.
						"known_hosts": "",
					},
				},
			},
		},
		"custom CA": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					CABundle: []byte("ca"),
					Repo:     "repo",
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"log.sh",
					},
					Args: []string{
						"fleet",
						"gitcloner",
						"repo",
						"/workspace",
						"--branch",
						"master",
						"--ca-bundle-file",
						"/gitjob/cabundle/" + bundleCAFile,
					},
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
					Env: []corev1.EnvVar{
						{
							Name:  fleetapply.JSONOutputEnvVar,
							Value: "true",
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
					Name: emptyDirVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: bundleCAVolumeName,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "gitrepo-cabundle",
						},
					},
				},
			},
			clientObjects: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "gitrepo-cabundle",
						Namespace: "default",
					},
					Data: map[string][]byte{
						"cacerts": []byte("foo"),
					},
					Type: corev1.SecretTypeSSHAuth,
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "known-hosts",
						Namespace: "cattle-fleet-system",
					},
					Data: map[string]string{
						// Prevent deployment error about config map not existing, but the data
						// does not matter in this test case.
						"known_hosts": "",
					},
				},
			},
		},
		"no custom CA but Rancher CA secret exists": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					Repo: "repo",
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"log.sh",
					},
					Args: []string{
						"fleet",
						"gitcloner",
						"repo",
						"/workspace",
						"--branch",
						"master",
						"--ca-bundle-file",
						"/gitjob/cabundle/" + bundleCAFile,
					},
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
					Env: []corev1.EnvVar{
						{
							Name:  fleetapply.JSONOutputEnvVar,
							Value: "true",
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
					Name: emptyDirVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: bundleCAVolumeName,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "gitrepo-cabundle",
						},
					},
				},
				{
					Name: "rancher-helm-secret-cert",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "gitrepo-rancher-cabundle",
							Items: []corev1.KeyToPath{
								{
									Key:  "cacerts",
									Path: "cacert.crt",
								},
							},
						},
					},
				},
			},
			expectedContainers: []corev1.Container{
				{
					Args: []string{
						"--cacerts-file",
						"/etc/rancher/certs/cacerts",
					},
					Name: "fleet",
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "rancher-helm-secret-cert",
							MountPath: "/etc/ssl/certs",
						},
					},
				},
			},
			clientObjects: []runtime.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "tls-ca-additional",
						Namespace: "cattle-system",
					},
					Data: map[string][]byte{
						"ca-additional.pem": []byte("foo"),
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "gitrepo-cabundle",
						Namespace: "default",
					},
					Data: map[string][]byte{
						"additional-ca.crt": []byte("foo"),
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "known-hosts",
						Namespace: "cattle-fleet-system",
					},
					Data: map[string]string{
						// Prevent deployment error about config map not existing, but the data
						// does not matter in this test case.
						"known_hosts": "",
					},
				},
			},
		},
		"skip tls": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					InsecureSkipTLSverify: true,
					Repo:                  "repo",
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"log.sh",
					},
					Args: []string{
						"fleet",
						"gitcloner",
						"repo",
						"/workspace",
						"--branch",
						"master",
						"--insecure-skip-tls",
					},
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
					Env: []corev1.EnvVar{
						{
							Name:  fleetapply.JSONOutputEnvVar,
							Value: "true",
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
					Name: emptyDirVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
			clientObjects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "known-hosts",
						Namespace: "cattle-fleet-system",
					},
					Data: map[string]string{
						// Prevent deployment error about config map not existing, but the data
						// does not matter in this test case.
						"known_hosts": "",
					},
				},
			},
		},
		"simple with tolerations": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{Repo: "repo"},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"log.sh",
					},
					Args: []string{
						"fleet",
						"gitcloner",
						"repo",
						"/workspace",
						"--branch",
						"master",
					},
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
					Env: []corev1.EnvVar{
						{
							Name:  fleetapply.JSONOutputEnvVar,
							Value: "true",
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
					Name: emptyDirVolumeName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
			deploymentTolerations: []corev1.Toleration{
				{
					Effect:   "NoSchedule",
					Key:      "key1",
					Value:    "value1",
					Operator: "Equals",
				},
				{
					Effect:   "NoExecute",
					Key:      "key2",
					Value:    "value2",
					Operator: "Exists",
				},
			},
			clientObjects: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "known-hosts",
						Namespace: "cattle-fleet-system",
					},
					Data: map[string]string{
						// Prevent deployment error about config map not existing, but the data
						// does not matter in this test case.
						"known_hosts": "",
					},
				},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			r := GitJobReconciler{
				Client:          getFakeClient(test.deploymentTolerations, test.clientObjects...),
				Scheme:          scheme,
				Image:           "test",
				Clock:           RealClock{},
				SystemNamespace: config.DefaultNamespace,
				KnownHosts:      ssh.KnownHosts{EnforceHostKeyChecks: test.strictHostKeyChecks},
			}

			job, err := r.newGitJob(ctx, test.gitrepo)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !cmp.Equal(job.Spec.Template.Spec.InitContainers, test.expectedInitContainers) {
				t.Fatalf("expected initContainers:\n\t%v,\n got:\n\t%v", test.expectedInitContainers, job.Spec.Template.Spec.InitContainers)
			}

			for _, eCont := range test.expectedContainers {
				found := false
				for _, cont := range job.Spec.Template.Spec.Containers {
					if cont.Name == eCont.Name {
						found = true

						for _, eArg := range eCont.Args {
							if argFound := slices.Contains(cont.Args, eArg); !argFound {
								t.Fatalf("expected arg %q not found in container %s with args %#v", eArg, eCont.Name, cont.Args)
							}
						}

						for _, eVM := range eCont.VolumeMounts {
							vmFound := false
							for _, vm := range cont.VolumeMounts {
								if vm.Name != eVM.Name {
									continue
								}
								vmFound = true
								if vm != eVM {
									t.Fatalf("expected volume mount %v in container %s, got %v", eVM, eCont.Name, vm)
								}
							}
							if !vmFound {
								t.Fatalf("expected volume mount %v not found in container %s", eVM, eCont.Name)
							}
						}
					}
				}
				if !found {
					t.Fatalf("expected container %s not found", eCont.Name)
				}
			}

			if len(test.expectedContainers) > 0 && len(test.expectedContainers) != len(job.Spec.Template.Spec.Containers) {
				t.Fatalf(
					"expected %d Containers:\n\t%v\ngot %d:\n\t%v",
					len(test.expectedContainers),
					test.expectedContainers,
					len(job.Spec.Template.Spec.Containers),
					job.Spec.Template.Spec.Containers,
				)
			}

			for _, expCont := range test.expectedContainers {
				found := false
				for _, cont := range job.Spec.Template.Spec.Containers {
					if cont.Name == expCont.Name {
						found = true

						for _, expMount := range expCont.VolumeMounts {
							foundMount := false
							for _, mount := range cont.VolumeMounts {
								if mount == expMount {
									foundMount = true
								}
							}

							if !foundMount {
								t.Fatalf("expected volume mount %v for container %v not found in\n\t%v", expMount, expCont, cont.VolumeMounts)
							}
						}
					}
				}

				if !found {
					t.Fatalf("expected container %v not found in\n\t%v", expCont, job.Spec.Template.Spec.Containers)
				}
			}

			for _, evol := range test.expectedVolumes {
				found := false
				for _, tvol := range job.Spec.Template.Spec.Volumes {
					if cmp.Equal(evol, tvol) {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("volume %v not found in \n\t%v", evol, job.Spec.Template.Spec.Volumes)
				}
			}

			// tolerations check
			// tolerations will be the default ones plus the deployment ones
			expectedTolerations := append(defaultTolerations, test.deploymentTolerations...)
			if !cmp.Equal(expectedTolerations, job.Spec.Template.Spec.Tolerations) {
				t.Fatalf("job tolerations differ. Expecting: %v and found: %v", test.deploymentTolerations, job.Spec.Template.Spec.Tolerations)
			}
		})
	}
}

func TestGenerateJob_EnvVars(t *testing.T) {
	ctx := context.TODO()

	tests := map[string]struct {
		gitrepo                      *fleetv1.GitRepo
		strictSSHHostKeyChecks       bool
		osEnv                        map[string]string
		expectedContainerEnvVars     []corev1.EnvVar
		expectedInitContainerEnvVars []corev1.EnvVar
	}{
		"Helm secret name": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					HelmSecretName: "foo",
					Repo:           "https://github.com/rancher/fleet-examples",
				},
				Status: fleetv1.GitRepoStatus{
					Commit: "commit",
				},
			},
			expectedContainerEnvVars: []corev1.EnvVar{
				{
					Name:  "HOME",
					Value: "/fleet-home",
				},
				{
					Name:  fleetapply.JSONOutputEnvVar,
					Value: "true",
				},
				{
					Name:  fleetapply.JobNameEnvVar,
					Value: "gitrepo-b7eaf",
				},
				{
					Name:  "FLEET_APPLY_CONFLICT_RETRIES",
					Value: "1",
				},
				{
					Name:  "FLEET_BUNDLE_CREATION_MAX_CONCURRENCY",
					Value: "4",
				},
				{
					Name:  "GIT_SSH_COMMAND",
					Value: "ssh -o stricthostkeychecking=no",
				},
				{
					Name: "HELM_USERNAME",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							Optional: &[]bool{true}[0],
							Key:      "username",
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "foo",
							},
						},
					},
				},
				{
					Name:  "COMMIT",
					Value: "commit",
				},
			},
			expectedInitContainerEnvVars: []corev1.EnvVar{
				{
					Name:  fleetapply.JSONOutputEnvVar,
					Value: "true",
				},
			},
		},
		"Helm secret name with strict host key checks": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					HelmSecretName: "foo",
					Repo:           "https://github.com/rancher/fleet-examples",
				},
				Status: fleetv1.GitRepoStatus{
					Commit: "commit",
				},
			},
			strictSSHHostKeyChecks: true,
			expectedInitContainerEnvVars: []corev1.EnvVar{
				{
					Name:  fleetapply.JSONOutputEnvVar,
					Value: "true",
				},
				{
					Name: "FLEET_KNOWN_HOSTS",
				},
			},
			expectedContainerEnvVars: []corev1.EnvVar{
				{
					Name:  "HOME",
					Value: "/fleet-home",
				},
				{
					Name:  fleetapply.JSONOutputEnvVar,
					Value: "true",
				},
				{
					Name:  fleetapply.JobNameEnvVar,
					Value: "gitrepo-b7eaf",
				},
				{
					Name:  "FLEET_APPLY_CONFLICT_RETRIES",
					Value: "1",
				},
				{
					Name:  "FLEET_BUNDLE_CREATION_MAX_CONCURRENCY",
					Value: "4",
				},
				{
					Name:  "GIT_SSH_COMMAND",
					Value: "ssh -o stricthostkeychecking=yes",
				},
				{
					Name: "HELM_USERNAME",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							Optional: &[]bool{true}[0],
							Key:      "username",
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "foo",
							},
						},
					},
				},
				{
					Name:  "COMMIT",
					Value: "commit",
				},
			},
		},
		"Helm secret name for paths": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					HelmSecretNameForPaths: "foo",
					Repo:                   "https://github.com/rancher/fleet-examples",
				},
				Status: fleetv1.GitRepoStatus{
					Commit: "commit",
				},
			},
			expectedContainerEnvVars: []corev1.EnvVar{
				{
					Name:  "HOME",
					Value: "/fleet-home",
				},
				{
					Name:  fleetapply.JSONOutputEnvVar,
					Value: "true",
				},
				{
					Name:  fleetapply.JobNameEnvVar,
					Value: "gitrepo-b7eaf",
				},
				{
					Name:  "FLEET_APPLY_CONFLICT_RETRIES",
					Value: "1",
				},
				{
					Name:  "FLEET_BUNDLE_CREATION_MAX_CONCURRENCY",
					Value: "4",
				},
				{
					Name:  "GIT_SSH_COMMAND",
					Value: "ssh -o stricthostkeychecking=no",
				},
				{
					Name:  "COMMIT",
					Value: "commit",
				},
			},
			expectedInitContainerEnvVars: []corev1.EnvVar{
				{
					Name:  fleetapply.JSONOutputEnvVar,
					Value: "true",
				},
			},
		},
		"Helm secret name for paths with strict host key checks": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					HelmSecretNameForPaths: "foo",
					Repo:                   "https://github.com/rancher/fleet-examples",
				},
				Status: fleetv1.GitRepoStatus{
					Commit: "commit",
				},
			},
			strictSSHHostKeyChecks: true,
			expectedInitContainerEnvVars: []corev1.EnvVar{
				{
					Name:  fleetapply.JSONOutputEnvVar,
					Value: "true",
				},
				{
					Name: "FLEET_KNOWN_HOSTS",
				},
			},
			expectedContainerEnvVars: []corev1.EnvVar{
				{
					Name:  "HOME",
					Value: "/fleet-home",
				},
				{
					Name:  fleetapply.JSONOutputEnvVar,
					Value: "true",
				},
				{
					Name:  fleetapply.JobNameEnvVar,
					Value: "gitrepo-b7eaf",
				},
				{
					Name:  "FLEET_APPLY_CONFLICT_RETRIES",
					Value: "1",
				},
				{
					Name:  "FLEET_BUNDLE_CREATION_MAX_CONCURRENCY",
					Value: "4",
				},
				{
					Name:  "GIT_SSH_COMMAND",
					Value: "ssh -o stricthostkeychecking=yes",
				},
				{
					Name:  "COMMIT",
					Value: "commit",
				},
			},
		},
		"proxy": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					Repo: "https://github.com/rancher/fleet-examples",
				},
				Status: fleetv1.GitRepoStatus{
					Commit: "commit",
				},
			},
			expectedContainerEnvVars: []corev1.EnvVar{
				{
					Name:  "HOME",
					Value: "/fleet-home",
				},
				{
					Name:  fleetapply.JSONOutputEnvVar,
					Value: "true",
				},
				{
					Name:  fleetapply.JobNameEnvVar,
					Value: "gitrepo-b7eaf",
				},
				{
					Name:  "FLEET_APPLY_CONFLICT_RETRIES",
					Value: "1",
				},
				{
					Name:  "FLEET_BUNDLE_CREATION_MAX_CONCURRENCY",
					Value: "4",
				},
				{
					Name:  "COMMIT",
					Value: "commit",
				},
				{
					Name:  "HTTP_PROXY",
					Value: "httpProxy",
				},
				{
					Name:  "HTTPS_PROXY",
					Value: "httpsProxy",
				},
			},
			expectedInitContainerEnvVars: []corev1.EnvVar{
				{
					Name:  fleetapply.JSONOutputEnvVar,
					Value: "true",
				},
				{
					Name:  "HTTP_PROXY",
					Value: "httpProxy",
				},
				{
					Name:  "HTTPS_PROXY",
					Value: "httpsProxy",
				},
			},
			osEnv: map[string]string{"HTTP_PROXY": "httpProxy", "HTTPS_PROXY": "httpsProxy"},
		},
		"retries_valid": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					Repo: "https://github.com/rancher/fleet-examples",
				},
				Status: fleetv1.GitRepoStatus{
					Commit: "commit",
				},
			},
			expectedContainerEnvVars: []corev1.EnvVar{
				{
					Name:  "HOME",
					Value: "/fleet-home",
				},
				{
					Name:  fleetapply.JSONOutputEnvVar,
					Value: "true",
				},
				{
					Name:  fleetapply.JobNameEnvVar,
					Value: "gitrepo-b7eaf",
				},
				{
					Name:  "FLEET_APPLY_CONFLICT_RETRIES",
					Value: "3",
				},
				{
					Name:  "FLEET_BUNDLE_CREATION_MAX_CONCURRENCY",
					Value: "4",
				},
				{
					Name:  "COMMIT",
					Value: "commit",
				},
			},
			osEnv: map[string]string{"FLEET_APPLY_CONFLICT_RETRIES": "3"},
			expectedInitContainerEnvVars: []corev1.EnvVar{
				{
					Name:  fleetapply.JSONOutputEnvVar,
					Value: "true",
				},
			},
		},
		"retries_not_valid": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					Repo: "https://github.com/rancher/fleet-examples",
				},
				Status: fleetv1.GitRepoStatus{
					Commit: "commit",
				},
			},
			expectedContainerEnvVars: []corev1.EnvVar{
				{
					Name:  "HOME",
					Value: "/fleet-home",
				},
				{
					Name:  fleetapply.JSONOutputEnvVar,
					Value: "true",
				},
				{
					Name:  fleetapply.JobNameEnvVar,
					Value: "gitrepo-b7eaf",
				},
				{
					Name:  "FLEET_APPLY_CONFLICT_RETRIES",
					Value: "1",
				},
				{
					Name:  "FLEET_BUNDLE_CREATION_MAX_CONCURRENCY",
					Value: "4",
				},
				{
					Name:  "COMMIT",
					Value: "commit",
				},
			},
			osEnv: map[string]string{"FLEET_APPLY_CONFLICT_RETRIES": "this_is_not_an_int"},
			expectedInitContainerEnvVars: []corev1.EnvVar{
				{
					Name:  fleetapply.JSONOutputEnvVar,
					Value: "true",
				},
			},
		},
		"bundle_creation_max_concurrency_valid": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					Repo: "https://github.com/rancher/fleet-examples",
				},
				Status: fleetv1.GitRepoStatus{
					Commit: "commit",
				},
			},
			expectedContainerEnvVars: []corev1.EnvVar{
				{
					Name:  "HOME",
					Value: "/fleet-home",
				},
				{
					Name:  fleetapply.JSONOutputEnvVar,
					Value: "true",
				},
				{
					Name:  fleetapply.JobNameEnvVar,
					Value: "gitrepo-b7eaf",
				},
				{
					Name:  "FLEET_APPLY_CONFLICT_RETRIES",
					Value: "1",
				},
				{
					Name:  "FLEET_BUNDLE_CREATION_MAX_CONCURRENCY",
					Value: "8",
				},
				{
					Name:  "COMMIT",
					Value: "commit",
				},
			},
			osEnv: map[string]string{"FLEET_BUNDLE_CREATION_MAX_CONCURRENCY": "8"},
			expectedInitContainerEnvVars: []corev1.EnvVar{
				{
					Name:  fleetapply.JSONOutputEnvVar,
					Value: "true",
				},
			},
		},
		"bundle_creation_max_concurrency_invalid": {
			gitrepo: &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "gitrepo",
					Namespace: "default",
				},
				Spec: fleetv1.GitRepoSpec{
					Repo: "https://github.com/rancher/fleet-examples",
				},
				Status: fleetv1.GitRepoStatus{
					Commit: "commit",
				},
			},
			expectedContainerEnvVars: []corev1.EnvVar{
				{
					Name:  "HOME",
					Value: "/fleet-home",
				},
				{
					Name:  fleetapply.JSONOutputEnvVar,
					Value: "true",
				},
				{
					Name:  fleetapply.JobNameEnvVar,
					Value: "gitrepo-b7eaf",
				},
				{
					Name:  "FLEET_APPLY_CONFLICT_RETRIES",
					Value: "1",
				},
				{
					Name:  "FLEET_BUNDLE_CREATION_MAX_CONCURRENCY",
					Value: "4",
				},
				{
					Name:  "COMMIT",
					Value: "commit",
				},
			},
			osEnv: map[string]string{"FLEET_BUNDLE_CREATION_MAX_CONCURRENCY": "this_is_not_an_int"},
			expectedInitContainerEnvVars: []corev1.EnvVar{
				{
					Name:  fleetapply.JSONOutputEnvVar,
					Value: "true",
				},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			for k, v := range test.osEnv {
				err := os.Setenv(k, v)
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}

			r := GitJobReconciler{
				Client:          getFakeClient([]corev1.Toleration{}),
				Image:           "test",
				Clock:           RealClock{},
				SystemNamespace: config.DefaultNamespace,
				KnownHosts: mockKnownHostsGetter{
					strict: test.strictSSHHostKeyChecks,
				},
			}
			job, err := r.newGitJob(ctx, test.gitrepo)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !cmp.Equal(job.Spec.Template.Spec.Containers[0].Env, test.expectedContainerEnvVars) {
				t.Errorf("unexpected envVars. expected %v, but got %v", test.expectedContainerEnvVars, job.Spec.Template.Spec.Containers[0].Env)
			}
			if !cmp.Equal(job.Spec.Template.Spec.InitContainers[0].Env, test.expectedInitContainerEnvVars) {
				t.Errorf("unexpected envVars. expected %v, but got %v", test.expectedInitContainerEnvVars, job.Spec.Template.Spec.InitContainers[0].Env)
			}

			for k := range test.osEnv {
				err := os.Unsetenv(k)
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

type mockKnownHostsGetter struct {
	data   string
	strict bool
	err    error
}

func (m mockKnownHostsGetter) Get(ctx context.Context, c client.Client, ns string, secretName string) (string, error) {
	return m.data, m.err
}

func (m mockKnownHostsGetter) IsStrict() bool {
	return m.strict
}

func TestGitClonerSSH(t *testing.T) {
	tests := map[string]struct {
		gitrepo               *fleetv1.GitRepo
		knownHostsData        string
		knownHostsErr         error
		expectedContainerArgs []string
		expectedErr           error
	}{
		"known hosts check would fail, non-SSH repo, no found known_hosts data": {
			gitrepo: &fleetv1.GitRepo{
				Spec: fleetv1.GitRepoSpec{
					Repo: "foo",
				},
			},
			// The error does not matter, as known hosts checks should not be called for non-SSH repos
			knownHostsErr: errors.New("something happened"),
			expectedContainerArgs: []string{
				"fleet",
				"gitcloner",
				"foo",
				"/workspace",
				"--branch",
				"master",
			},
			expectedErr: nil,
		},
		"known hosts check passes, SSH repo, no found known_hosts data": {
			gitrepo: &fleetv1.GitRepo{
				Spec: fleetv1.GitRepoSpec{
					Repo: "ssh://foo",
				},
			},
			expectedContainerArgs: []string{
				"fleet",
				"gitcloner",
				"ssh://foo",
				"/workspace",
				"--branch",
				"master",
			},
			expectedErr: nil,
		},
		"known hosts check would pass, non-SSH repo, no found known_hosts data": {
			gitrepo: &fleetv1.GitRepo{
				Spec: fleetv1.GitRepoSpec{
					Repo: "foo",
				},
			},
			expectedContainerArgs: []string{
				"fleet",
				"gitcloner",
				"foo",
				"/workspace",
				"--branch",
				"master",
			},
			expectedErr: nil,
		},
		"SSH repo, found known_hosts data": {
			gitrepo: &fleetv1.GitRepo{
				Spec: fleetv1.GitRepoSpec{
					Repo: "ssh://foo",
				},
			},
			knownHostsData: "foo",
			expectedContainerArgs: []string{
				"fleet",
				"gitcloner",
				"ssh://foo",
				"/workspace",
				"--branch",
				"master",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			r := GitJobReconciler{
				Client: fake.NewFakeClient(),
				Image:  "test",
				KnownHosts: mockKnownHostsGetter{
					data: test.knownHostsData,
					err:  test.knownHostsErr,
				},
			}

			cont, err := r.newGitCloner(context.TODO(), test.gitrepo, test.knownHostsData)
			if (err != nil && test.expectedErr == nil) || (err == nil && test.expectedErr != nil) {
				t.Errorf("expecting error %v, got %v", test.expectedErr, err)
			}
			if err != nil && test.expectedErr != nil && err.Error() != test.expectedErr.Error() {
				t.Errorf("expecting error %v, got %v", test.expectedErr, err)
			}
			if len(cont.Args) != len(test.expectedContainerArgs) {
				t.Fatalf("expecting args %v, got %v", test.expectedContainerArgs, cont.Args)
			}

			for idx := range test.expectedContainerArgs {
				if cont.Args[idx] != test.expectedContainerArgs[idx] {
					t.Errorf("expecting arg %q at index %d, got %q", test.expectedContainerArgs[idx], idx, cont.Args[idx])

				}
			}
		})
	}
}

func TestDrivenScanSeparator(t *testing.T) {
	tests := map[string]struct {
		bundles        []fleetv1.BundlePath
		expectError    bool
		expectedResult string
	}{
		"Bundle definitions have no separator character": {
			bundles: []fleetv1.BundlePath{
				{
					Base:    "test/one/two",
					Options: "options.yaml",
				},
				{
					Base:    "test/",
					Options: "options2.yaml",
				},
			},
			expectError:    false,
			expectedResult: ":",
		},
		"Bundle definitions have : separator character": {
			bundles: []fleetv1.BundlePath{
				{
					Base:    "test/one:two",
					Options: "options.yaml",
				},
				{
					Base:    "test/",
					Options: "options2.yaml",
				},
			},
			expectError:    false,
			expectedResult: ",",
		},
		"Bundle definitions have : and , separator characters": {
			bundles: []fleetv1.BundlePath{
				{
					Base:    "test/one:two",
					Options: "options.yaml",
				},
				{
					Base:    "test,one",
					Options: "options2.yaml",
				},
			},
			expectError:    false,
			expectedResult: "|",
		},
		"Bundle definitions have : , and | separator characters": {
			bundles: []fleetv1.BundlePath{
				{
					Base:    "test/one:two",
					Options: "options.yaml",
				},
				{
					Base:    "test,one",
					Options: "options2|.yaml",
				},
			},
			expectError:    false,
			expectedResult: "?",
		},
		"Bundle definitions have : ,  | and ? separator characters": {
			bundles: []fleetv1.BundlePath{
				{
					Base:    "test?one:two",
					Options: "options.yaml",
				},
				{
					Base:    "test,one",
					Options: "options2|.yaml",
				},
			},
			expectError:    false,
			expectedResult: "<",
		},
		"Bundle definitions have : ,  |  ? and < separator characters": {
			bundles: []fleetv1.BundlePath{
				{
					Base:    "test?one:two",
					Options: "options.yaml",
				},
				{
					Base:    "test,one<",
					Options: "options2|.yaml",
				},
			},
			expectError:    false,
			expectedResult: ">",
		},
		"Bundle definitions have all separator characters": {
			bundles: []fleetv1.BundlePath{
				{
					Base:    "test?one:two",
					Options: "options.yaml",
				},
				{
					Base:    "test,one<>",
					Options: "options2|.yaml",
				},
			},
			expectError:    true,
			expectedResult: "",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			gitrepo := &fleetv1.GitRepo{
				Spec: fleetv1.GitRepoSpec{
					Bundles: test.bundles,
				},
			}
			separator, err := getDrivenScanSeparator(*gitrepo)
			if !test.expectError {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if test.expectedResult != separator {
					t.Errorf("expecting separator to be: %q, got: %q", test.expectedResult, separator)
				}
			} else {
				expectedErrorMessage := fmt.Sprintf("bundle base and/or options paths contain all possible characters from %q, please update those paths to remedy this", bundleOptionsSeparatorChars)
				if err == nil {
					t.Errorf("expecting error, got none")
				}
				if err.Error() != expectedErrorMessage {
					t.Errorf("expecting error %q, got %q", expectedErrorMessage, err.Error())
				}
			}
		})
	}
}

func TestFilterFleetApplyJobOutput(t *testing.T) {
	tests := map[string]struct {
		input          string
		expectedOutput string
	}{
		"Filter a few lines and return a couple from Fleet": {
			input: `this line should be ignored
		this line should be ignored, too
		{"level":"fatal","msg":"This line is not from fleet cli","time":"2025-04-15T14:53:15+02:00"}
		{"fleetErrorMessage":"fleet line 1","level":"fatal","msg":"Fleet cli failed","time":"2025-04-15T14:53:15+02:00"}
		ignore this line as well
		{"fleetErrorMessage":"fleet line 2","level":"fatal","msg":"Fleet cli failed","time":"2025-04-15T14:53:15+02:00"}`,
			expectedOutput: "fleet line 1\nfleet line 2",
		},
		"There are no lines from fleet apply": {
			input: `this line should be ignored
		this line should be ignored, too
		{"level":"fatal","msg":"This line is not from fleet cli","time":"2025-04-15T14:53:15+02:00"}
		ignore this line as well`,
			expectedOutput: "Unknown error",
		},
		"The output is from fleet apply, but it's not in json format": {
			input:          "FATA[0000] no resource found at the following paths to deploy: [tt]",
			expectedOutput: "Unknown error",
		},
		"Valid message with some extra text before and after": {
			input: `this line should be ignored
		this line should be ignored, too
		This line is OK{"fleetErrorMessage":"fleet line error 1","level":"fatal","msg":"Fleet cli failed","time":"2025-04-15T14:53:15+02:00"}This part should be ignored
		ignore this line as well`,
			expectedOutput: "fleet line error 1",
		},
		"Valid message with some extra text before": {
			input: `this line should be ignored
		this line should be ignored, too
		This line is OK{"fleetErrorMessage":"fleet line error 1","level":"fatal","msg":"Fleet cli failed","time":"2025-04-15T14:53:15+02:00"}
		ignore this line as well`,
			expectedOutput: "fleet line error 1",
		},
		"Valid message with some extra text after": {
			input: `this line should be ignored
		this line should be ignored, too
		This line is OK{"fleetErrorMessage":"fleet line error 1","level":"fatal","msg":"Fleet cli failed","time":"2025-04-15T14:53:15+02:00"}
		ignore this line as well`,
			expectedOutput: "fleet line error 1",
		},
		"Not valid json message": {
			input: `this line should be ignored
		this line should be ignored, too
		This lin}e is OK "{fleetErrorMessage":"fleet line error 1","level":"fatal","msg":"Fleet cli failed","time":"2025-04-15T14:53:15+02:00"}
		ignore this line as well`,
			expectedOutput: "Unknown error",
		},
		"More than one error in the same line": {
			input: `this line should be ignored
this line should be ignored, too
T}his{ lin}e is OK "{"fleetErrorMessage":"fleet line error 1","level":"fatal","msg":"Fleet cli failed","time":"2025-04-15T14:53:15+02:00"}more garbage{"fleetErrorMessage":"fleet line error 2","level":"fatal","msg":"Fleet cli failed","time":"2025-04-15T14:53:15+02:00"}some more noise
ignore this line as well`,
			expectedOutput: "fleet line error 1\nfleet line error 2",
		},
		"Empty input": {
			input:          "",
			expectedOutput: "Unknown error",
		},

		"The fleetErrorMessage field is empty": {
			input:          `{"fleetErrorMessage":"","level":"fatal","msg":"Fleet cli failed","time":"2025-04-15T14:53:15+02:00"}`,
			expectedOutput: "Unknown error",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			filteredMsg := filterFleetCLIJobOutput(test.input)
			if filteredMsg != test.expectedOutput {
				t.Errorf("expecting output %q, got %q", test.expectedOutput, filteredMsg)
			}
		})
	}
}

func getFakeClient(tolerations []corev1.Toleration, objs ...runtime.Object) client.Client {
	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(appsv1.AddToScheme(scheme))

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(objs...).
		WithRuntimeObjects(getFleetControllerDeployment(tolerations)).Build()
}

func getFleetControllerDeployment(tolerations []corev1.Toleration) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.ManagerConfigName,
			Namespace: config.DefaultNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Tolerations: tolerations,
				},
			},
		},
	}
}
