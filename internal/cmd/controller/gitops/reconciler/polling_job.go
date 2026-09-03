// Copyright (c) 2025 SUSE LLC

package reconciler

import (
	"context"
	"fmt"
	"time"

	"github.com/reugn/go-quartz/quartz"
	"golang.org/x/sync/semaphore"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/rancher/wrangler/v3/pkg/kstatus"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var _ quartz.Job = &gitPollingJob{}

type gitPollingJob struct {
	sem    *semaphore.Weighted
	client client.Client

	namespace string
	name      string

	repo   string
	branch string

	recorder   events.EventRecorder
	gitFetcher GitFetcher
}

func newGitPollingJob(c client.Client, r events.EventRecorder, repo fleet.GitRepo, fetcher GitFetcher) *gitPollingJob {
	return &gitPollingJob{
		sem:        semaphore.NewWeighted(1),
		client:     c,
		recorder:   r,
		gitFetcher: fetcher,

		namespace: repo.Namespace,
		name:      repo.Name,

		repo:   repo.Spec.Repo,
		branch: repo.Spec.Branch,
	}
}

func (j *gitPollingJob) Execute(ctx context.Context) error {
	logger := log.FromContext(ctx)

	if !j.sem.TryAcquire(1) {
		// already running
		logger.V(1).Info("skipping polling job execution: already running")

		return nil
	}
	defer j.sem.Release(1)

	err := j.pollGitRepo(ctx)
	if err != nil {
		// Quartz schedulers are built with the default logger.NoOpLogger and no
		// job retries, so an error returned from here is discarded. Log it
		// before handing it back, or polling failures are invisible.
		logger.Error(err, "GitRepo polling job failed, retrying on the next poll")
	}

	return err
}

// Description returns a description for the job.
// This is needed to implement the Quartz Job interface.
func (j *gitPollingJob) Description() string {
	return fmt.Sprintf("gitops-polling-%s-%s-%s-%s", j.namespace, j.name, j.repo, j.branch)
}

func (j *gitPollingJob) pollGitRepo(ctx context.Context) error {
	gitrepo := &fleet.GitRepo{}
	nsName := types.NamespacedName{
		Name:      j.name,
		Namespace: j.namespace,
	}
	if err := j.client.Get(ctx, nsName, gitrepo); err != nil {
		return fmt.Errorf("could not get GitRepo resource from polling job: %w", err)
	}

	pollingTimestamp := time.Now().UTC()

	fail := func(origErr error) error {
		j.recorder.Eventf(
			gitrepo,
			nil,
			corev1.EventTypeWarning,
			"FailedToCheckCommit",
			"CheckCommit",
			"%v",
			origErr,
		)

		return j.updateErrorStatus(ctx, gitrepo, pollingTimestamp, origErr)
	}

	commit, err := monitorLatestCommit(gitrepo, func() (string, error) {
		return j.gitFetcher.LatestCommit(ctx, gitrepo, j.client)
	})
	if err != nil {
		return fail(err)
	}

	if commit != gitrepo.Status.Commit {
		j.recorder.Eventf(
			gitrepo,
			nil,
			corev1.EventTypeNormal,
			"GotNewCommit",
			"GetNewCommit",
			commit,
		)
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.GitRepo{}
		if err := j.client.Get(ctx, nsName, t); err != nil {
			return fmt.Errorf("could not get GitRepo to update its status: %w", err)
		}

		orig := t.DeepCopy()

		t.Status.LastPollingTime = metav1.Time{Time: pollingTimestamp}
		t.Status.PollingCommit = commit

		condition.Cond(gitPollingCondition).SetError(&t.Status, "", nil)

		if equality.Semantic.DeepEqual(orig.Status, t.Status) {
			// skip update if nothing changed
			return nil
		}

		// Patch under an optimistic lock, against a base read in this same attempt.
		// A merge patch replaces the whole conditions array, so patching from a
		// stale base drops conditions written concurrently by the gitjob
		// reconciler. RetryOnConflict re-reads and recomputes on conflict.
		return j.client.Status().Patch(
			ctx,
			t,
			client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{}),
		)
	})
	if err != nil {
		// The commit check itself succeeded here; only the status write lost a
		// race. Reporting that through fail() would emit a FailedToCheckCommit
		// event and mark the GitRepo stalled over a transient conflict. Return
		// the error instead, for Execute to log: the next scheduled poll
		// recomputes the status from a fresh read.
		return fmt.Errorf("could not update GitRepo status with polling timestamp: %w", err)
	}

	return nil
}

// updateErrorStatus updates the provided gitrepo's status to reflect the provided orgErr.
// This includes updating the gitrepo's polling timestamp, if provided.
func (j *gitPollingJob) updateErrorStatus(
	ctx context.Context,
	gitrepo *fleet.GitRepo,
	pollingTimestamp time.Time,
	orgErr error,
) error {
	nsn := types.NamespacedName{Name: gitrepo.Name, Namespace: gitrepo.Namespace}

	merr := []error{orgErr}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.GitRepo{}
		if err := j.client.Get(ctx, nsn, t); err != nil {
			return fmt.Errorf("could not get GitRepo to update its status: %w", err)
		}

		orig := t.DeepCopy()

		condition.Cond(gitPollingCondition).SetError(&t.Status, "", orgErr)
		kstatus.SetError(t, orgErr.Error())

		if !pollingTimestamp.IsZero() {
			t.Status.LastPollingTime = metav1.Time{Time: pollingTimestamp}
		}

		if equality.Semantic.DeepEqual(orig.Status, t.Status) {
			return nil
		}

		// See pollGitRepo: patch under an optimistic lock against a base read in
		// this same attempt, so a stale base cannot drop foreign conditions.
		return j.client.Status().Patch(
			ctx,
			t,
			client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{}),
		)
	})
	if err != nil {
		merr = append(merr, err)
	}
	return errutil.NewAggregate(merr)
}
