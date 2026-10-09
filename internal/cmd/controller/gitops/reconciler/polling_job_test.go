package reconciler

import (
	"context"
	"errors"
	"testing"

	"github.com/rancher/fleet/internal/mocks"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	gitmocks "github.com/rancher/fleet/pkg/git/mocks"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	"go.uber.org/mock/gomock"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestPollGitRepo(t *testing.T) {
	const (
		namespace = "test-ns"
		name      = "test-repo"
		repoURL   = "https://github.com/rancher/fleet-examples"
		branch    = "main"
	)

	type testcase struct {
		name            string
		gitrepo         *v1alpha1.GitRepo
		setupMocks      func(*mocks.MockK8sClient, *mocks.MockStatusWriter, *gitmocks.MockGitFetcher, *events.FakeRecorder)
		patchErr        error
		patchTimes      int // number of expected Status().Patch() calls; 0 means 1
		expectedErr     string
		expectedEvents  []string
		validateGitRepo func(*testing.T, *v1alpha1.GitRepo)
	}

	testCases := []testcase{
		{
			name: "New commit found",
			gitrepo: &v1alpha1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec:       v1alpha1.GitRepoSpec{Repo: repoURL, Branch: branch},
				Status:     v1alpha1.GitRepoStatus{Commit: "old-commit"},
			},
			setupMocks: func(c *mocks.MockK8sClient, sw *mocks.MockStatusWriter, gf *gitmocks.MockGitFetcher, r *events.FakeRecorder) {
				nsName := types.NamespacedName{Name: name, Namespace: namespace}
				c.EXPECT().Get(gomock.Any(), nsName, gomock.Any()).Times(2).DoAndReturn(func(_ context.Context, _ types.NamespacedName, obj *v1alpha1.GitRepo, _ ...client.GetOption) error {
					obj.Name = name
					obj.Namespace = namespace
					obj.Spec.Repo = repoURL
					obj.Spec.Branch = branch
					obj.Status.Commit = "old-commit"
					return nil
				})
				gf.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return("new-commit", nil)
				c.EXPECT().Status().Return(sw)
			},
			expectedEvents: []string{"Normal GotNewCommit new-commit"},
			validateGitRepo: func(t *testing.T, gr *v1alpha1.GitRepo) {
				t.Helper()
				if gr.Status.PollingCommit != "new-commit" {
					t.Errorf("expected PollingCommit to be 'new-commit', got %s", gr.Status.PollingCommit)
				}
				cond := findStatusCondition(gr.Status.Conditions, gitPollingCondition)
				if cond == nil || cond.Status != "True" {
					t.Errorf("expected GitPolling condition to be True")
				}
			},
		},
		{
			name: "No new commit",
			gitrepo: &v1alpha1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec:       v1alpha1.GitRepoSpec{Repo: repoURL, Branch: branch},
				Status:     v1alpha1.GitRepoStatus{Commit: "same-commit"},
			},
			setupMocks: func(c *mocks.MockK8sClient, sw *mocks.MockStatusWriter, gf *gitmocks.MockGitFetcher, r *events.FakeRecorder) {
				c.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Times(2).DoAndReturn(func(_ context.Context, _ client.ObjectKey, obj *v1alpha1.GitRepo, _ ...client.GetOption) error {
					obj.Name = name
					obj.Namespace = namespace
					obj.Spec.Repo = repoURL
					obj.Spec.Branch = branch
					obj.Status.Commit = "same-commit"
					return nil
				})
				gf.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return("same-commit", nil)
				c.EXPECT().Status().Return(sw)
			},
			validateGitRepo: func(t *testing.T, gr *v1alpha1.GitRepo) {
				t.Helper()
				if gr.Status.PollingCommit != "same-commit" {
					t.Errorf("expected PollingCommit to be 'same-commit', got %s", gr.Status.PollingCommit)
				}
			},
		},
		{
			// A failed poll sets Stalled. With an unchanged commit, no gitjob
			// reconcile follows the next successful poll, so the poller must clear
			// the Stalled condition it set itself. Reconciling, set since by the
			// gitjob reconciler for a job in progress, must be kept.
			name: "Successful poll clears Stalled set by a failed poll",
			gitrepo: &v1alpha1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec:       v1alpha1.GitRepoSpec{Repo: repoURL, Branch: branch},
			},
			setupMocks: func(c *mocks.MockK8sClient, sw *mocks.MockStatusWriter, gf *gitmocks.MockGitFetcher, r *events.FakeRecorder) {
				c.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Times(2).DoAndReturn(func(_ context.Context, _ client.ObjectKey, obj *v1alpha1.GitRepo, _ ...client.GetOption) error {
					obj.Name = name
					obj.Namespace = namespace
					obj.Spec.Repo = repoURL
					obj.Spec.Branch = branch
					obj.Status.Commit = "same-commit"
					obj.Status.Conditions = append(
						failedPollConditions("context deadline exceeded", "context deadline exceeded"),
						genericcondition.GenericCondition{Type: "Reconciling", Status: "True", Reason: "Reconciling"},
					)
					return nil
				})
				gf.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return("same-commit", nil)
				c.EXPECT().Status().Return(sw)
			},
			validateGitRepo: func(t *testing.T, gr *v1alpha1.GitRepo) {
				t.Helper()
				if cond := findStatusCondition(gr.Status.Conditions, gitPollingCondition); cond == nil || cond.Status != "True" {
					t.Errorf("expected GitPolling condition to be True, got %+v", cond)
				}
				if cond := findStatusCondition(gr.Status.Conditions, "Stalled"); cond == nil || cond.Status != "False" || cond.Message != "" {
					t.Errorf("expected Stalled condition to be cleared, got %+v", cond)
				}
				if cond := findStatusCondition(gr.Status.Conditions, "Reconciling"); cond == nil || cond.Status != "True" {
					t.Errorf("expected Reconciling condition to be kept True, got %+v", cond)
				}
			},
		},
		{
			// Stalled with a message other than the polling error was set by the
			// gitjob reconciler, e.g. for a failed clone job. A successful poll
			// says nothing about that failure, so it must stay.
			name: "Successful poll keeps Stalled set by a failed clone job",
			gitrepo: &v1alpha1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec:       v1alpha1.GitRepoSpec{Repo: repoURL, Branch: branch},
			},
			setupMocks: func(c *mocks.MockK8sClient, sw *mocks.MockStatusWriter, gf *gitmocks.MockGitFetcher, r *events.FakeRecorder) {
				c.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Times(2).DoAndReturn(func(_ context.Context, _ client.ObjectKey, obj *v1alpha1.GitRepo, _ ...client.GetOption) error {
					obj.Name = name
					obj.Namespace = namespace
					obj.Spec.Repo = repoURL
					obj.Spec.Branch = branch
					obj.Status.Commit = "same-commit"
					obj.Status.Conditions = failedPollConditions(
						"context deadline exceeded",
						"no resource found at the following paths to deploy: [.]",
					)
					return nil
				})
				gf.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return("same-commit", nil)
				c.EXPECT().Status().Return(sw)
			},
			validateGitRepo: func(t *testing.T, gr *v1alpha1.GitRepo) {
				t.Helper()
				if cond := findStatusCondition(gr.Status.Conditions, gitPollingCondition); cond == nil || cond.Status != "True" {
					t.Errorf("expected GitPolling condition to be True, got %+v", cond)
				}
				cond := findStatusCondition(gr.Status.Conditions, "Stalled")
				if cond == nil || cond.Status != "True" || cond.Message != "no resource found at the following paths to deploy: [.]" {
					t.Errorf("expected Stalled condition from the clone job to be kept, got %+v", cond)
				}
			},
		},
		{
			// A failed poll overwrites the message of Stalled set for a failed
			// job, so the messages match. The job is still failed, and nothing
			// re-runs the gitjob reconciler on an unchanged commit, so Stalled
			// must stay.
			name: "Successful poll keeps Stalled while the clone job is failed",
			gitrepo: &v1alpha1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec:       v1alpha1.GitRepoSpec{Repo: repoURL, Branch: branch},
			},
			setupMocks: func(c *mocks.MockK8sClient, sw *mocks.MockStatusWriter, gf *gitmocks.MockGitFetcher, r *events.FakeRecorder) {
				c.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Times(2).DoAndReturn(func(_ context.Context, _ client.ObjectKey, obj *v1alpha1.GitRepo, _ ...client.GetOption) error {
					obj.Name = name
					obj.Namespace = namespace
					obj.Spec.Repo = repoURL
					obj.Spec.Branch = branch
					obj.Status.Commit = "same-commit"
					obj.Status.GitJobStatus = "Failed"
					obj.Status.Conditions = failedPollConditions("context deadline exceeded", "context deadline exceeded")
					return nil
				})
				gf.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return("same-commit", nil)
				c.EXPECT().Status().Return(sw)
			},
			validateGitRepo: func(t *testing.T, gr *v1alpha1.GitRepo) {
				t.Helper()
				if cond := findStatusCondition(gr.Status.Conditions, gitPollingCondition); cond == nil || cond.Status != "True" {
					t.Errorf("expected GitPolling condition to be True, got %+v", cond)
				}
				if cond := findStatusCondition(gr.Status.Conditions, "Stalled"); cond == nil || cond.Status != "True" {
					t.Errorf("expected Stalled condition to be kept for the failed job, got %+v", cond)
				}
			},
		},
		{
			name: "Git fetch error",
			gitrepo: &v1alpha1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec:       v1alpha1.GitRepoSpec{Repo: repoURL, Branch: branch},
			},
			setupMocks: func(c *mocks.MockK8sClient, sw *mocks.MockStatusWriter, gf *gitmocks.MockGitFetcher, r *events.FakeRecorder) {
				nsName := types.NamespacedName{Name: name, Namespace: namespace}
				c.EXPECT().Get(gomock.Any(), nsName, gomock.Any()).Times(2).DoAndReturn(func(_ context.Context, _ client.ObjectKey, obj *v1alpha1.GitRepo, _ ...client.GetOption) error {
					obj.Name = name
					obj.Namespace = namespace
					obj.Spec.Repo = repoURL
					obj.Spec.Branch = branch
					obj.Status.Commit = "commit"
					return nil
				})
				gf.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return("", errors.New("git error"))
				c.EXPECT().Status().Return(sw)
			},
			expectedErr:    "git error",
			expectedEvents: []string{"Warning FailedToCheckCommit git error"},
			validateGitRepo: func(t *testing.T, gr *v1alpha1.GitRepo) {
				t.Helper()
				cond := findStatusCondition(gr.Status.Conditions, gitPollingCondition)
				if cond == nil || cond.Status != "False" || cond.Message != "git error" {
					t.Errorf("expected GitPolling condition to be False with message 'git error', got %+v", cond)
				}
			},
		},
		{
			name: "Update status error",
			gitrepo: &v1alpha1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec:       v1alpha1.GitRepoSpec{Repo: repoURL, Branch: branch},
			},
			setupMocks: func(c *mocks.MockK8sClient, sw *mocks.MockStatusWriter, gf *gitmocks.MockGitFetcher, r *events.FakeRecorder) {
				nsName := types.NamespacedName{Name: name, Namespace: namespace}
				c.EXPECT().Get(gomock.Any(), nsName, gomock.Any()).Times(2).DoAndReturn(func(_ context.Context, _ client.ObjectKey, obj *v1alpha1.GitRepo, _ ...client.GetOption) error {
					obj.Name = name
					obj.Namespace = namespace
					obj.Spec.Repo = repoURL
					obj.Spec.Branch = branch
					obj.Status.Commit = "commit"
					return nil
				})
				gf.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return("new-commit", nil)
				c.EXPECT().Status().Return(sw)
			},
			patchErr:    errors.New("update error"),
			expectedErr: "could not update GitRepo status with polling timestamp: update error",
			// Only the commit event: a failed status write must not additionally be
			// reported as a failed commit check.
			expectedEvents: []string{"Normal GotNewCommit new-commit"},
		},
		{
			// The status patch is made under an optimistic lock, so a concurrent
			// writer makes it conflict. Losing that race says nothing about the
			// commit check, which already succeeded, so it must not emit a
			// FailedToCheckCommit event or mark the GitRepo as stalled.
			name: "Status update conflict",
			gitrepo: &v1alpha1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec:       v1alpha1.GitRepoSpec{Repo: repoURL, Branch: branch},
			},
			setupMocks: func(c *mocks.MockK8sClient, sw *mocks.MockStatusWriter, gf *gitmocks.MockGitFetcher, r *events.FakeRecorder) {
				nsName := types.NamespacedName{Name: name, Namespace: namespace}
				// One read up front, then one per retry attempt.
				c.EXPECT().Get(gomock.Any(), nsName, gomock.Any()).Times(1 + retry.DefaultRetry.Steps).DoAndReturn(func(_ context.Context, _ client.ObjectKey, obj *v1alpha1.GitRepo, _ ...client.GetOption) error {
					obj.Name = name
					obj.Namespace = namespace
					obj.Spec.Repo = repoURL
					obj.Spec.Branch = branch
					obj.Status.Commit = "commit"
					return nil
				})
				gf.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).Return("new-commit", nil)
				c.EXPECT().Status().Return(sw).Times(retry.DefaultRetry.Steps)
			},
			patchErr: apierrors.NewConflict(
				schema.GroupResource{Group: "fleet.cattle.io", Resource: "gitrepos"},
				name,
				errors.New("the object has been modified"),
			),
			patchTimes: retry.DefaultRetry.Steps,
			expectedErr: "could not update GitRepo status with polling timestamp: " +
				`Operation cannot be fulfilled on gitrepos.fleet.cattle.io "test-repo": the object has been modified`,
			expectedEvents: []string{"Normal GotNewCommit new-commit"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockClient := mocks.NewMockK8sClient(ctrl)
			mockStatusWriter := mocks.NewMockStatusWriter(ctrl)
			mockGitFetcher := gitmocks.NewMockGitFetcher(ctrl)
			recorder := events.NewFakeRecorder(10)

			if tc.setupMocks != nil {
				tc.setupMocks(mockClient, mockStatusWriter, mockGitFetcher, recorder)
			}

			job := newGitPollingJob(mockClient, recorder, *tc.gitrepo, mockGitFetcher)

			// We are testing pollGitRepo directly, which modifies the gitrepo object in place for status updates.
			// So we need a mechanism to capture the final state of the gitrepo.
			// The patch function in the mock will be our capture point.
			var finalGitRepo *v1alpha1.GitRepo

			patchTimes := tc.patchTimes
			if patchTimes == 0 {
				patchTimes = 1
			}

			mockStatusWriter.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, obj *v1alpha1.GitRepo, _ client.Patch, _ ...client.PatchOption) error {
					finalGitRepo = obj.DeepCopy()
					return tc.patchErr
				}).Times(patchTimes)

			err := job.pollGitRepo(context.Background())

			if tc.expectedErr != "" {
				if err == nil || err.Error() != tc.expectedErr {
					t.Errorf("expected error '%q', got '%v'", tc.expectedErr, err)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			close(recorder.Events)
			if len(recorder.Events) != len(tc.expectedEvents) {
				t.Errorf("expected %d events, got %d", len(tc.expectedEvents), len(recorder.Events))
			}
			for i, expectedEvent := range tc.expectedEvents {
				if event, ok := <-recorder.Events; ok && event != expectedEvent {
					t.Errorf("expected event %d to be '%q', got '%q'", i, expectedEvent, event)
				}
			}

			if tc.validateGitRepo != nil {
				// The gitrepo passed to pollGitRepo is modified in-place for status updates.
				// We use the captured object from the Patch mock.
				if finalGitRepo == nil {
					t.Fatal("Status().Patch() was not called, cannot validate final gitrepo state")
				}
				tc.validateGitRepo(t, finalGitRepo)
			}
		})
	}
}

func TestGitPollingJob_Description(t *testing.T) {
	job := newGitPollingJob(nil, nil, v1alpha1.GitRepo{
		ObjectMeta: metav1.ObjectMeta{Name: "test-repo", Namespace: "test-ns"},
		Spec:       v1alpha1.GitRepoSpec{Repo: "http://a.b/c.git", Branch: "develop"},
	}, nil)

	expected := "gitops-polling-test-ns-test-repo-http://a.b/c.git-develop"
	if job.Description() != expected {
		t.Errorf("expected description '%q', got '%q'", expected, job.Description())
	}
}

// failedPollConditions returns a False GitPolling condition with pollMsg and a
// True Stalled condition with stalledMsg. updateErrorStatus sets both to the
// poll error; a different stalledMsg means another writer set Stalled.
func failedPollConditions(pollMsg, stalledMsg string) []genericcondition.GenericCondition {
	return []genericcondition.GenericCondition{
		{Type: gitPollingCondition, Status: "False", Reason: "Error", Message: pollMsg},
		{Type: "Stalled", Status: "True", Reason: "Stalled", Message: stalledMsg},
	}
}

// findStatusCondition finds the conditionType in conditions.
func findStatusCondition(conditions []genericcondition.GenericCondition, conditionType string) *genericcondition.GenericCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}

	return nil
}
