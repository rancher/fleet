package poll

import (
	"context"
	"sync"
	"time"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"

	"k8s.io/client-go/util/retry"

	v1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	"github.com/rancher/gitjob/pkg/git"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const defaultSyncInterval = 15

type GitFetcher interface {
	LatestCommit(ctx context.Context, gitjob *v1.GitJob, client client.Client) (string, error)
}

// Watch fetches the latest commit of a git repository referenced by a gitJob with the syncInterval provided.
type Watch struct {
	gitJob  v1.GitJob
	client  client.Client
	done    chan bool
	mu      *sync.Mutex
	fetcher GitFetcher
	log     logr.Logger
}

func NewWatch(gitJob v1.GitJob, client client.Client) Watcher {
	return &Watch{
		gitJob:  gitJob,
		client:  client,
		mu:      new(sync.Mutex),
		fetcher: &git.Fetch{},
		log:     ctrl.Log.WithName("git-latest-commit-poll-watch"),
	}
}

// StartBackgroundSync fetches the latest commit every syncInternal in a goroutine.
func (w *Watch) StartBackgroundSync(ctx context.Context) {
	go w.fetchBySyncInterval(ctx, time.NewTicker(calculateSyncInterval(w.gitJob)))
}

// Finish stops watching for changes in the git repo.
func (w *Watch) Finish() {
	w.done <- true
}

func (w *Watch) Restart(ctx context.Context) {
	w.Finish()
	w.StartBackgroundSync(ctx)
}

func (w *Watch) UpdateGitJob(gitJob v1.GitJob) {
	w.gitJob = gitJob
}

func (w *Watch) GetSyncInterval() int {
	return w.gitJob.Spec.SyncInterval
}

func (w *Watch) fetchBySyncInterval(ctx context.Context, ticker *time.Ticker) {
	w.log.V(1).Info("start watching latest commit", "gitjob-name", w.gitJob.Name)
	defer ticker.Stop()
	w.done = make(chan bool)
	w.fetchLatestCommitAndUpdateStatus(ctx)

	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			w.mu.Lock()
			w.fetchLatestCommitAndUpdateStatus(ctx)
			w.mu.Unlock()
		}
	}
}

func (w *Watch) fetchLatestCommitAndUpdateStatus(ctx context.Context) {
	commit, err := w.fetcher.LatestCommit(ctx, &w.gitJob, w.client)
	if err != nil {
		w.log.Error(err, "error fetching commit", "gitjob name", w.gitJob.Name)
		return
	}
	if w.gitJob.Status.Commit != commit {
		w.log.Info("new commit found", "gitjob name", w.gitJob.Name, "commit", commit)
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var gitJobFomCluster v1.GitJob
			err := w.client.Get(ctx, types.NamespacedName{Name: w.gitJob.Name, Namespace: w.gitJob.Namespace}, &gitJobFomCluster)
			if err != nil {
				return err
			}
			gitJobFomCluster.Status.Commit = commit

			return w.client.Status().Update(ctx, &gitJobFomCluster)
		}); err != nil {
			w.log.Error(err, "error updating status when a new commit was found by polling", "gitjob", w.gitJob)
		}
	}
}

func calculateSyncInterval(gitJob v1.GitJob) time.Duration {
	if gitJob.Spec.SyncInterval != 0 {
		return time.Duration(gitJob.Spec.SyncInterval) * time.Second
	}

	return time.Duration(defaultSyncInterval) * time.Second
}
