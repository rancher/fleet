package poll

import (
	"context"
	stderrors "errors"
	"time"

	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/git"
	"github.com/reugn/go-quartz/quartz"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	maxSchedulerOperationRetries = 3
	defaultSyncInterval          = 15 * time.Second
)

type Watcher interface {
	StartBackgroundSync(ctx context.Context)
	Finish()
	Restart(ctx context.Context)
	UpdateGitRepo(gitRepo v1alpha1.GitRepo)
	GetSyncInterval() float64
}

// Handler handles all the watches for the git repositories. These watches are pulling the latest commit every syncPeriod.
type Handler struct {
	client    client.Client
	log       logr.Logger
	scheduler quartz.Scheduler
	fetcher   GitFetcher
}

func NewHandler(ctx context.Context, client client.Client) *Handler {
	scheduler := quartz.NewStdScheduler()
	scheduler.Start(ctx)
	return &Handler{
		client:    client,
		log:       ctrl.Log.WithName("git-latest-commit-poll-handler"),
		scheduler: scheduler,
		fetcher:   &git.Fetch{},
	}
}

// AddOrModifyGitRepoPollJob adds a new scheduled job for the gitrepo if no job was already present.
// It updates the existing job for this gitrepo if present.
func (h *Handler) AddOrModifyGitRepoPollJob(ctx context.Context, gitRepo v1alpha1.GitRepo) {
	gitRepoPollKey := GitRepoPollKey(gitRepo)
	scheduledJob, err := h.scheduler.GetScheduledJob(gitRepoPollKey)
	if err != nil {
		// job was not found
		if gitRepo.Spec.DisablePolling {
			// nothing to do if disablePolling is set
			return
		}
		h.scheduleJob(ctx, gitRepoPollKey, gitRepo, true)
	} else {
		if gitRepo.Spec.DisablePolling {
			// if polling is disabled, just delete the job from the scheduler
			err = h.scheduler.DeleteJob(gitRepoPollKey)
			if err != nil {
				h.log.Error(err, "error deleting the job", "job", gitRepoPollKey)
			}
			return
		}
		job := scheduledJob.JobDetail().Job()
		gitRepoPollJob, ok := job.(*GitRepoPollJob)
		if !ok {
			h.log.Error(stderrors.New("invalid job"),
				"error getting Gitrepo poll job, the scheduled job is not a GitRepoPollJob", "job", job.Description())
			return
		}
		previousInterval := gitRepoPollJob.GitRepo.Spec.PollingInterval
		previousGeneration := gitRepoPollJob.GitRepo.Generation
		gitRepoPollJob.GitRepo = gitRepo
		if (previousGeneration != gitRepo.Generation) ||
			!durations.Equal(gitRepo.Spec.PollingInterval, previousInterval) {
			// Spec or polling interval changed
			// Reschedule so the job is immediately executed
			// (otherwise it'll wait until next timeout)
			_ = h.scheduler.DeleteJob(gitRepoPollKey)
			h.scheduleJob(ctx, gitRepoPollKey, gitRepo, true)
		}
	}
}

// CleanUpGitRepoPollJobs removes all poll jobs whose gitrepo is not present in the cluster.
func (h *Handler) CleanUpGitRepoPollJobs(ctx context.Context) {
	var gitRepo v1alpha1.GitRepo
	for _, key := range h.scheduler.GetJobKeys() {
		namespacedName := types.NamespacedName{
			Namespace: key.Group(),
			Name:      key.Name(),
		}
		if err := h.client.Get(ctx, namespacedName, &gitRepo); errors.IsNotFound(err) {
			err = h.scheduler.DeleteJob(key)
			if err != nil {
				h.log.Error(err, "error deleting job", "job", key)
			}
		}
	}
}

func calculateSyncInterval(gitRepo v1alpha1.GitRepo) time.Duration {
	if gitRepo.Spec.PollingInterval != nil {
		return gitRepo.Spec.PollingInterval.Duration
	}

	return defaultSyncInterval
}

func (h *Handler) scheduleJob(ctx context.Context, jobKey *quartz.JobKey, gitRepo v1alpha1.GitRepo, runBefore bool) {
	job := NewGitRepoPollJob(h.client, h.fetcher, gitRepo)
	if runBefore {
		// ignoring error because that is only used by the quartz library to implement its retries mechanism
		// The GitRepoPollJob always returns nil
		_ = job.Execute(ctx)
	}
	err := h.scheduler.ScheduleJob(quartz.NewJobDetail(job, jobKey),
		quartz.NewSimpleTrigger(calculateSyncInterval(gitRepo)))
	if err != nil {
		h.log.Error(err, "error scheduling job", "job", jobKey)
	}
}
