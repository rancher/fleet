//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/poller_mock.go -package=mocks github.com/rancher/fleet/internal/cmd/controller/gitops/reconciler GitPoller
//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/client_mock.go -package=mocks sigs.k8s.io/controller-runtime/pkg/client Client,SubResourceWriter

package reconciler

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.uber.org/mock/gomock"

	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	"github.com/rancher/fleet/internal/mocks"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"

	gitmocks "github.com/rancher/fleet/pkg/git/mocks"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type ClockMock struct {
	t time.Time
}

func (m ClockMock) Now() time.Time                  { return m.t }
func (m ClockMock) Since(t time.Time) time.Duration { return m.t.Sub(t) }

func getGitPollingCondition(gitrepo *fleetv1.GitRepo) (genericcondition.GenericCondition, bool) {
	for _, cond := range gitrepo.Status.Conditions {
		if cond.Type == gitPollingCondition {
			return cond, true
		}
	}
	return genericcondition.GenericCondition{}, false
}

func TestReconcile_ReturnsAndRequeuesAfterAddingFinalizer(t *testing.T) {
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
	client := mocks.NewMockClient(mockCtrl)
	client.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, gitrepo *fleetv1.GitRepo, opts ...interface{}) error {
			gitrepo.Name = gitRepo.Name
			gitrepo.Namespace = gitRepo.Namespace
			gitrepo.Spec.Repo = "repo"
			return nil
		},
	)
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	fetcher := gitmocks.NewMockGitFetcher(mockCtrl)
	client.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, repo *fleetv1.GitRepo, opts ...interface{}) {
			// check that we added the finalizer
			if !controllerutil.ContainsFinalizer(repo, finalize.GitRepoFinalizer) {
				t.Errorf("expecting gitrepo to contain finalizer")
			}
		},
	).Times(1)

	r := GitJobReconciler{
		Client:     client,
		Scheme:     scheme,
		Image:      "",
		GitFetcher: fetcher,
		Clock:      RealClock{},
	}

	ctx := context.TODO()

	// second call is the one calling LatestCommit
	res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	if !res.Requeue {
		t.Errorf("expecting Requeue set to true, it was false")
	}
}

func TestReconcile_LatestCommitErrorIsSetInConditions(t *testing.T) {
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
	client := mocks.NewMockClient(mockCtrl)
	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	client.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, gitrepo *fleetv1.GitRepo, opts ...interface{}) error {
			gitrepo.Name = gitRepo.Name
			gitrepo.Namespace = gitRepo.Namespace
			gitrepo.Spec.Repo = "repo"
			controllerutil.AddFinalizer(gitrepo, finalize.GitRepoFinalizer)
			return nil
		},
	)
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	client.EXPECT().Status().Return(statusClient).Times(1)
	fetcher := gitmocks.NewMockGitFetcher(mockCtrl)
	fetcher.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return("", fmt.Errorf("TEST ERROR"))
	statusClient.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, repo *fleetv1.GitRepo, opts ...interface{}) {
			cond, found := getGitPollingCondition(repo)
			if !found {
				t.Errorf("expecting Condition %s to be found", gitPollingCondition)
			}
			if cond.Message != "TEST ERROR" {
				t.Errorf("expecting condition message [TEST ERROR], got [%s]", cond.Message)
			}
			if cond.Type != gitPollingCondition {
				t.Errorf("expecting condition type [%s], got [%s]", gitPollingCondition, cond.Type)
			}
			if cond.Status != "False" {
				t.Errorf("expecting condition Status [False], got [%s]", cond.Type)
			}
		},
	).Times(1)

	r := GitJobReconciler{
		Client:     client,
		Scheme:     scheme,
		Image:      "",
		GitFetcher: fetcher,
		Clock:      RealClock{},
	}

	ctx := context.TODO()

	// second call is the one calling LatestCommit
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
}

func TestReconcile_LatestCommitIsOkay(t *testing.T) {
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
	client := mocks.NewMockClient(mockCtrl)
	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	client.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, gitrepo *fleetv1.GitRepo, opts ...interface{}) error {
			gitrepo.Name = gitRepo.Name
			gitrepo.Namespace = gitRepo.Namespace
			gitrepo.Spec.Repo = "repo"
			controllerutil.AddFinalizer(gitrepo, finalize.GitRepoFinalizer)
			return nil
		},
	)
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	client.EXPECT().Status().Return(statusClient).Times(1)

	fetcher := gitmocks.NewMockGitFetcher(mockCtrl)
	commit := "1883fd54bc5dfd225acf02aecbb6cb8020458e33"
	fetcher.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(commit, nil)
	statusClient.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, repo *fleetv1.GitRepo, opts ...interface{}) {
			cond, found := getGitPollingCondition(repo)
			if !found {
				t.Errorf("expecting Condition %s to be found", gitPollingCondition)
			}
			if cond.Message != "" {
				t.Errorf("expecting condition message empty, got [%s]", cond.Message)
			}
			if cond.Type != gitPollingCondition {
				t.Errorf("expecting condition type [%s], got [%s]", gitPollingCondition, cond.Type)
			}
			if cond.Status != "True" {
				t.Errorf("expecting condition Status [True], got [%s]", cond.Type)
			}
			if repo.Status.Commit != commit {
				t.Errorf("expecting commit %s, got %s", commit, repo.Status.Commit)
			}
		},
	).Times(1)

	r := GitJobReconciler{
		Client:     client,
		Scheme:     scheme,
		Image:      "",
		GitFetcher: fetcher,
		Clock:      RealClock{},
	}

	ctx := context.TODO()

	// second call is the one calling LatestCommit
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
}

func TestReconcile_LatestCommitNotCalledYet(t *testing.T) {
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
	client := mocks.NewMockClient(mockCtrl)
	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	client.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, gitrepo *fleetv1.GitRepo, opts ...interface{}) error {
			gitrepo.Name = gitRepo.Name
			gitrepo.Namespace = gitRepo.Namespace
			gitrepo.Spec.Repo = "repo"
			controllerutil.AddFinalizer(gitrepo, finalize.GitRepoFinalizer)

			// set last polling time to now...
			// default gitrepo polling time is 15 seconds, so it won't call LatestCommit this time
			gitrepo.Status.LastPollingTime.Time = time.Now()
			return nil
		},
	)
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	client.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, repo *fleetv1.GitRepo, opts ...interface{}) {
			if repo.Status.Commit != "" {
				t.Errorf("expecting gitrepo empty commit, got [%s]", repo.Status.Commit)
			}
			cond, found := getGitPollingCondition(repo)
			if found {
				t.Errorf("not expecting Condition %s to be found. Got [%s]", gitPollingCondition, cond)
			}
		},
	).Times(1)

	fetcher := gitmocks.NewMockGitFetcher(mockCtrl)
	r := GitJobReconciler{
		Client:     client,
		Scheme:     scheme,
		Image:      "",
		GitFetcher: fetcher,
		Clock:      RealClock{},
	}

	ctx := context.TODO()

	// second call is the one calling LatestCommit
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
}

func TestReconcile_LatestCommitShouldBeCalled(t *testing.T) {
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
	client := mocks.NewMockClient(mockCtrl)
	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	client.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	fetcher := gitmocks.NewMockGitFetcher(mockCtrl)
	commit := "1883fd54bc5dfd225acf02aecbb6cb8020458e33"
	fetcher.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(commit, nil)
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, gitrepo *fleetv1.GitRepo, opts ...interface{}) error {
			gitrepo.Name = gitRepo.Name
			gitrepo.Namespace = gitRepo.Namespace
			gitrepo.Spec.Repo = "repo"
			controllerutil.AddFinalizer(gitrepo, finalize.GitRepoFinalizer)

			// set last polling time to now less 15 seconds (which is the default)
			// that should trigger the polling job now
			now := time.Now()
			gitrepo.Status.LastPollingTime.Time = now.Add(time.Duration(-15) * time.Second)
			// commit is something different to what we expect after this reconcile
			gitrepo.Status.Commit = "dd45c7ad68e10307765104fea4a1f5997643020f"
			return nil
		},
	)
	client.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	client.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Update(gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, repo *fleetv1.GitRepo, opts ...interface{}) {
			cond, found := getGitPollingCondition(repo)
			if !found {
				t.Errorf("expecting Condition %s to be found", gitPollingCondition)
			}
			if cond.Message != "" {
				t.Errorf("expecting condition message empty, got [%s]", cond.Message)
			}
			if cond.Type != gitPollingCondition {
				t.Errorf("expecting condition type [%s], got [%s]", gitPollingCondition, cond.Type)
			}
			if cond.Status != "True" {
				t.Errorf("expecting condition Status [True], got [%s]", cond.Type)
			}
			if repo.Status.Commit != commit {
				t.Errorf("expecting commit %s, got %s", commit, repo.Status.Commit)
			}
		},
	).Times(1)

	r := GitJobReconciler{
		Client:     client,
		Scheme:     scheme,
		Image:      "",
		GitFetcher: fetcher,
		Clock:      RealClock{},
	}

	ctx := context.TODO()

	// second call is the one calling LatestCommit
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
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
	mockClient := mocks.NewMockClient(mockCtrl)
	mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(ctx context.Context, restrictions *fleetv1.GitRepoRestrictionList, ns client.InNamespace) error {
			// fill the restrictions with a couple of allowed namespaces.
			// As the gitrepo has no target namespace restrictions won't be met
			restriction := fleetv1.GitRepoRestriction{AllowedTargetNamespaces: []string{"ns1", "ns2"}}
			restrictions.Items = append(restrictions.Items, restriction)
			return nil
		},
	)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
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

	r := GitJobReconciler{
		Client: mockClient,
		Scheme: scheme,
		Image:  "",
		Clock:  RealClock{},
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

func TestNewJob(t *testing.T) { // nolint:funlen
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
	ctx := context.TODO()

	tests := map[string]struct {
		gitrepo                *fleetv1.GitRepo
		client                 client.Client
		expectedInitContainers []corev1.Container
		expectedVolumes        []corev1.Volume
		expectedErr            error
	}{
		"simple (no credentials, no ca, no skip tls)": {
			gitrepo: &fleetv1.GitRepo{
				Spec: fleetv1.GitRepoSpec{Repo: "repo"},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"fleet",
					},
					Args:  []string{"gitcloner", "repo", "/workspace", "--branch", "master"},
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
		"simple with custom branch": {
			gitrepo: &fleetv1.GitRepo{
				Spec: fleetv1.GitRepoSpec{
					Repo:   "repo",
					Branch: "foo",
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"fleet",
					},
					Args:  []string{"gitcloner", "repo", "/workspace", "--branch", "foo"},
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
		"simple with custom revision": {
			gitrepo: &fleetv1.GitRepo{
				Spec: fleetv1.GitRepoSpec{
					Repo:     "repo",
					Revision: "foo",
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"fleet",
					},
					Args:  []string{"gitcloner", "repo", "/workspace", "--revision", "foo"},
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
			gitrepo: &fleetv1.GitRepo{
				Spec: fleetv1.GitRepoSpec{
					Repo:             "repo",
					ClientSecretName: "secretName",
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"fleet",
					},
					Args: []string{
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
			gitrepo: &fleetv1.GitRepo{
				Spec: fleetv1.GitRepoSpec{
					Repo:             "repo",
					ClientSecretName: "secretName",
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"fleet",
					},
					Args: []string{
						"gitcloner",
						"repo",
						"/workspace",
						"--branch",
						"master",
						"--ssh-private-key-file",
						"/gitjob/ssh/" + corev1.SSHAuthPrivateKey,
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
			client: sshSecretMock(),
		},
		"custom CA": {
			gitrepo: &fleetv1.GitRepo{
				Spec: fleetv1.GitRepoSpec{
					CABundle: []byte("ca"),
					Repo:     "repo",
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"fleet",
					},
					Args: []string{
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
			gitrepo: &fleetv1.GitRepo{
				Spec: fleetv1.GitRepoSpec{
					InsecureSkipTLSverify: true,
					Repo:                  "repo",
				},
			},
			expectedInitContainers: []corev1.Container{
				{
					Command: []string{
						"fleet",
					},
					Args: []string{
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
				Client: test.client,
				Scheme: scheme,
				Image:  "test",
				Clock:  RealClock{},
			}
			job, err := r.newGitJob(ctx, test.gitrepo)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !cmp.Equal(job.Spec.Template.Spec.InitContainers, test.expectedInitContainers) {
				t.Fatalf("expected initContainers: %v, got: %v", test.expectedInitContainers, job.Spec.Template.Spec.InitContainers)
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
					t.Fatalf("volume %v not found in %v", evol, job.Spec.Template.Spec.Volumes)
				}
			}
		})
	}
}

func TestGenerateJob_EnvVars(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	ctx := context.TODO()
	poller := mocks.NewMockGitPoller(mockCtrl)
	poller.EXPECT().AddOrModifyGitRepoPollJob(ctx, gomock.Any()).AnyTimes()
	poller.EXPECT().CleanUpGitRepoPollJobs(ctx).AnyTimes()

	tests := map[string]struct {
		gitrepo                      *fleetv1.GitRepo
		osEnv                        map[string]string
		expectedContainerEnvVars     []corev1.EnvVar
		expectedInitContainerEnvVars []corev1.EnvVar
	}{
		"Helm secret name for paths": {
			gitrepo: &fleetv1.GitRepo{
				Spec: fleetv1.GitRepoSpec{
					HelmSecretNameForPaths: "foo",
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
					Name:  "GIT_SSH_COMMAND",
					Value: "ssh -o stricthostkeychecking=accept-new",
				},
				{
					Name:  "COMMIT",
					Value: "commit",
				},
			},
		},
		"proxy": {
			gitrepo: &fleetv1.GitRepo{
				Spec: fleetv1.GitRepoSpec{},
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
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			r := GitJobReconciler{
				Client: fake.NewFakeClient(),
				Image:  "test",
				Clock:  RealClock{},
			}
			for k, v := range test.osEnv {
				err := os.Setenv(k, v)
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
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

func TestCheckforPollingTask(t *testing.T) {
	tests := map[string]struct {
		gitrepo        *fleetv1.GitRepo
		timeNow        time.Time
		expectedResult bool
	}{
		"LastPollingTime is not set": {
			gitrepo:        &fleetv1.GitRepo{},
			timeNow:        time.Now(), // time here is irrelevant
			expectedResult: true,
		},
		"LastPollingTime is set but should still not trigger (1s away)": {
			gitrepo: &fleetv1.GitRepo{
				Status: fleetv1.GitRepoStatus{
					LastPollingTime: metav1.Time{Time: time.Date(2024, time.July, 16, 15, 59, 59, 0, time.UTC)},
				},
				Spec: fleetv1.GitRepoSpec{
					PollingInterval: &metav1.Duration{Duration: 10 * time.Second},
				},
			},
			timeNow:        time.Date(2024, time.July, 16, 16, 0, 0, 0, time.UTC),
			expectedResult: false,
		},
		"LastPollingTime is set and should trigger (10s away)": {
			gitrepo: &fleetv1.GitRepo{
				Status: fleetv1.GitRepoStatus{
					LastPollingTime: metav1.Time{Time: time.Date(2024, time.July, 16, 15, 59, 50, 0, time.UTC)},
				},
				Spec: fleetv1.GitRepoSpec{
					PollingInterval: &metav1.Duration{Duration: 10 * time.Second},
				},
			},
			timeNow:        time.Date(2024, time.July, 16, 16, 0, 0, 0, time.UTC),
			expectedResult: true,
		},
		"LastPollingTime is set but should still not trigger (1s away with default value)": {
			gitrepo: &fleetv1.GitRepo{
				Status: fleetv1.GitRepoStatus{
					LastPollingTime: metav1.Time{Time: time.Date(2024, time.July, 16, 15, 59, 59, 0, time.UTC)},
				},
			},
			timeNow:        time.Date(2024, time.July, 16, 16, 0, 0, 0, time.UTC),
			expectedResult: false,
		},
		"LastPollingTime is set and should trigger (15s away with default value)": {
			gitrepo: &fleetv1.GitRepo{
				Status: fleetv1.GitRepoStatus{
					LastPollingTime: metav1.Time{Time: time.Date(2024, time.July, 16, 15, 59, 45, 0, time.UTC)},
				},
			},
			timeNow:        time.Date(2024, time.July, 16, 16, 0, 0, 0, time.UTC),
			expectedResult: true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()
			fetcher := gitmocks.NewMockGitFetcher(mockCtrl)
			commit := "1883fd54bc5dfd225acf02aecbb6cb8020458e33"
			if test.expectedResult {
				fetcher.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(commit, nil)
			}
			r := GitJobReconciler{
				Client:     fake.NewFakeClient(),
				Image:      "test",
				Clock:      ClockMock{t: test.timeNow},
				GitFetcher: fetcher,
			}
			res, err := r.checkPollingTask(context.TODO(), test.gitrepo)
			if res != test.expectedResult {
				t.Errorf("unexpected result. Expecting %t, got %t", test.expectedResult, res)
			}
			if err != nil {
				t.Errorf("not expecting to get an error, got [%v]", err)
			}
			if res {
				// if the task was called, commit will be applied
				if test.gitrepo.Status.Commit != commit {
					t.Errorf("expecting commit: %s, got: %s", commit, test.gitrepo.Status.Commit)
				}
				// also LastPollingTime should be set to now
				if test.gitrepo.Status.LastPollingTime.Time != test.timeNow {
					t.Errorf("expecting LastPollingTime to be: %s, got: %s", test.timeNow, test.gitrepo.Status.LastPollingTime.Time)
				}
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
