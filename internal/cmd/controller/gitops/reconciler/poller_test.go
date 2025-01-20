//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/poller_mock.go -package=mocks github.com/rancher/fleet/internal/cmd/controller/gitops/reconciler GitPoller
//go:generate mockgen --build_flags=--mod=mod -destination=../../../../mocks/client_mock.go -package=mocks sigs.k8s.io/controller-runtime/pkg/client Client,SubResourceWriter

package reconciler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	"github.com/rancher/fleet/internal/mocks"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetevent "github.com/rancher/fleet/pkg/event"
	gitmocks "github.com/rancher/fleet/pkg/git/mocks"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	"go.uber.org/mock/gomock"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	mockClient := mocks.NewMockClient(mockCtrl)
	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, gitrepo *fleetv1.GitRepo, opts ...interface{}) error {
			gitrepo.Name = gitRepo.Name
			gitrepo.Namespace = gitRepo.Namespace
			gitrepo.Spec.Repo = "repo"
			controllerutil.AddFinalizer(gitrepo, finalize.GitRepoFinalizer)
			return nil
		},
	)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)
	fetcher := gitmocks.NewMockGitFetcher(mockCtrl)
	fetcher.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return("", fmt.Errorf("TEST ERROR"))
	statusClient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, repo *fleetv1.GitRepo, patch client.Patch, opts ...interface{}) {
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

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)
	recorderMock.EXPECT().Event(
		&gitRepoMatcher{gitRepo},
		fleetevent.Warning,
		"FailedToCheckCommit",
		"TEST ERROR",
	)
	r := PollerReconciler{
		Client:     mockClient,
		Scheme:     scheme,
		GitFetcher: fetcher,
		Clock:      RealClock{},
		Recorder:   recorderMock,
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
	mockClient := mocks.NewMockClient(mockCtrl)
	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, gitrepo *fleetv1.GitRepo, opts ...interface{}) error {
			gitrepo.Name = gitRepo.Name
			gitrepo.Namespace = gitRepo.Namespace
			gitrepo.Spec.Repo = "repo"
			controllerutil.AddFinalizer(gitrepo, finalize.GitRepoFinalizer)
			return nil
		},
	)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)

	fetcher := gitmocks.NewMockGitFetcher(mockCtrl)
	commit := "1883fd54bc5dfd225acf02aecbb6cb8020458e33"
	fetcher.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(commit, nil)
	statusClient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, repo *fleetv1.GitRepo, patch client.Patch, opts ...interface{}) {
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
			if repo.Status.PollerCommit != commit {
				t.Errorf("expecting commit %s, got %s", commit, repo.Status.PollerCommit)
			}
		},
	).Times(1)

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)
	recorderMock.EXPECT().Event(
		&gitRepoMatcher{gitRepo},
		fleetevent.Normal,
		"GotNewCommit",
		"1883fd54bc5dfd225acf02aecbb6cb8020458e33",
	)

	r := PollerReconciler{
		Client:     mockClient,
		Scheme:     scheme,
		GitFetcher: fetcher,
		Clock:      RealClock{},
		Recorder:   recorderMock,
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
	mockClient := mocks.NewMockClient(mockCtrl)
	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
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
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, repo *fleetv1.GitRepo, patch client.Patch, opts ...interface{}) {
			if repo.Status.PollerCommit != "" {
				t.Errorf("expecting gitrepo empty commit, got [%s]", repo.Status.PollerCommit)
			}
			cond, found := getGitPollingCondition(repo)
			if found {
				t.Errorf("not expecting Condition %s to be found. Got [%s]", gitPollingCondition, cond)
			}
		},
	).Times(1)

	fetcher := gitmocks.NewMockGitFetcher(mockCtrl)
	r := PollerReconciler{
		Client:     mockClient,
		Scheme:     scheme,
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
	mockClient := mocks.NewMockClient(mockCtrl)
	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	fetcher := gitmocks.NewMockGitFetcher(mockCtrl)
	commit := "1883fd54bc5dfd225acf02aecbb6cb8020458e33"
	fetcher.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(commit, nil)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
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
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, repo *fleetv1.GitRepo, patch client.Patch, opts ...interface{}) {
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
			if repo.Status.PollerCommit != commit {
				t.Errorf("expecting commit %s, got %s", commit, repo.Status.PollerCommit)
			}
		},
	).Times(1)

	recorderMock := mocks.NewMockEventRecorder(mockCtrl)
	recorderMock.EXPECT().Event(
		&gitRepoMatcher{gitRepo},
		fleetevent.Normal,
		"GotNewCommit",
		"1883fd54bc5dfd225acf02aecbb6cb8020458e33",
	)

	r := PollerReconciler{
		Client:     mockClient,
		Scheme:     scheme,
		GitFetcher: fetcher,
		Clock:      RealClock{},
		Recorder:   recorderMock,
	}

	ctx := context.TODO()

	// second call is the one calling LatestCommit
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
}

func TestReconcile_GenerationChanged(t *testing.T) {
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
	statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
	mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(ctx context.Context, req types.NamespacedName, gitrepo *fleetv1.GitRepo, opts ...interface{}) error {
			gitrepo.Name = gitRepo.Name
			gitrepo.Namespace = gitRepo.Namespace
			gitrepo.Spec.Repo = "repo"
			controllerutil.AddFinalizer(gitrepo, finalize.GitRepoFinalizer)
			gitrepo.Status.LastPollingTime.Time = time.Now()
			gitrepo.Generation = 10
			gitrepo.Status.PollerGeneration = 9
			return nil
		},
	)
	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	mockClient.EXPECT().Status().Return(statusClient).Times(1)
	statusClient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Do(
		func(ctx context.Context, repo *fleetv1.GitRepo, patch client.Patch, opts ...interface{}) {
			if repo.Status.PollerCommit != "" {
				t.Errorf("expecting gitrepo empty commit, got [%s]", repo.Status.PollerCommit)
			}
			cond, found := getGitPollingCondition(repo)
			if found {
				t.Errorf("not expecting Condition %s to be found. Got [%s]", gitPollingCondition, cond)
			}
			if repo.Status.PollerGeneration != repo.Generation {
				t.Errorf("expecting pollerGeneration to be %d. Got [%d]", repo.Generation, repo.Status.PollerGeneration)
			}
		},
	).Times(1)

	fetcher := gitmocks.NewMockGitFetcher(mockCtrl)
	r := PollerReconciler{
		Client:     mockClient,
		Scheme:     scheme,
		GitFetcher: fetcher,
		Clock:      RealClock{},
	}

	ctx := context.TODO()

	// second call is the one calling LatestCommit
	result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: namespacedName})
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}

	if result.RequeueAfter != time.Millisecond*5 {
		t.Errorf("unexpected requeue after value %s", result.RequeueAfter)
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

	fillTestGitRepo := func(gitrepo *fleetv1.GitRepo, testGitRepo *fleetv1.GitRepo) {
		gitrepo.Name = "gitrepo"
		gitrepo.Namespace = "ns"
		gitrepo.Spec.Repo = "repo"
		controllerutil.AddFinalizer(gitrepo, finalize.GitRepoFinalizer)
		gitrepo.Status.LastPollingTime = testGitRepo.Status.LastPollingTime
		gitrepo.Spec.PollingInterval = testGitRepo.Spec.PollingInterval
	}

	getTestGitRepo := func(testGitRepo *fleetv1.GitRepo) fleetv1.GitRepo {
		gitrepo := &fleetv1.GitRepo{}
		fillTestGitRepo(gitrepo, testGitRepo)
		return *gitrepo
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()
			mockClient := mocks.NewMockClient(mockCtrl)
			mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
			mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
				func(ctx context.Context, req types.NamespacedName, gitrepo *fleetv1.GitRepo, opts ...interface{}) error {
					fillTestGitRepo(gitrepo, test.gitrepo)
					return nil
				},
			)
			commit := "1883fd54bc5dfd225acf02aecbb6cb8020458e33"

			statusClient := mocks.NewMockSubResourceWriter(mockCtrl)
			mockClient.EXPECT().Status().Return(statusClient).Times(1)
			statusClient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Do(
				func(ctx context.Context, repo *fleetv1.GitRepo, patch client.Patch, opts ...interface{}) {
					if test.expectedResult {
						if repo.Status.PollerCommit != commit {
							t.Errorf("expecting commit %s, got %s", commit, repo.Status.PollerCommit)
						}
						// also LastPollingTime should be set to now
						if repo.Status.LastPollingTime.Time != test.timeNow {
							t.Errorf("expecting LastPollingTime to be: %s, got: %s", test.timeNow, repo.Status.LastPollingTime.Time)
						}
					} else {
						// if polling was not executed lastPollingTime should be the same value
						if repo.Status.LastPollingTime.Time != test.gitrepo.Status.LastPollingTime.Time {
							t.Errorf("expecting LastPollingTime to be: %s, got: %s", test.gitrepo.Status.LastPollingTime.Time, repo.Status.LastPollingTime.Time)
						}
					}
				},
			).Times(1)
			recorderMock := mocks.NewMockEventRecorder(mockCtrl)
			fetcher := gitmocks.NewMockGitFetcher(mockCtrl)

			if test.expectedResult {
				fetcher.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(commit, nil)
				recorderMock.EXPECT().Event(
					&gitRepoMatcher{getTestGitRepo(test.gitrepo)},
					fleetevent.Normal,
					"GotNewCommit",
					commit,
				)
			}
			r := PollerReconciler{
				Client:     mockClient,
				Clock:      ClockMock{t: test.timeNow},
				GitFetcher: fetcher,
				Recorder:   recorderMock,
			}
			// res, err := r.repoPolled(context.TODO(), test.gitrepo)
			namespacedName := types.NamespacedName{}
			_, err := r.Reconcile(context.TODO(), ctrl.Request{NamespacedName: namespacedName})
			if err != nil {
				t.Errorf("not expecting to get an error, got [%v]", err)
			}
		})
	}
}
