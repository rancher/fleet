package poll

import (
	"context"
	"sync"
	"time"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"

	"k8s.io/client-go/util/retry"

	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/git"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const defaultSyncInterval = 15

type GitFetcher interface {
	LatestCommit(ctx context.Context, gitrepo *v1alpha1.GitRepo, client client.Client) (string, error)
}

// Watch fetches the latest commit of a git repository referenced by a gitRepo with the syncInterval provided.
type Watch struct {
	gitRepo v1alpha1.GitRepo
	client  client.Client
	done    chan bool
	mu      *sync.Mutex
	fetcher GitFetcher
	log     logr.Logger
}

func NewWatch(gitRepo v1alpha1.GitRepo, client client.Client) Watcher {
	return &Watch{
		gitRepo: gitRepo,
		client:  client,
		mu:      new(sync.Mutex),
		fetcher: &git.Fetch{},
		log:     ctrl.Log.WithName("git-latest-commit-poll-watch"),
	}
}

// StartBackgroundSync fetches the latest commit every syncInternal in a goroutine.
func (w *Watch) StartBackgroundSync(ctx context.Context) {
	go w.fetchBySyncInterval(ctx, time.NewTicker(calculateSyncInterval(w.gitRepo)))
}

// Finish stops watching for changes in the git repo.
func (w *Watch) Finish() {
	w.done <- true
}

func (w *Watch) Restart(ctx context.Context) {
	w.Finish()
	w.StartBackgroundSync(ctx)
}

func (w *Watch) UpdateGitRepo(gitRepo v1alpha1.GitRepo) {
	w.gitRepo = gitRepo
}

func (w *Watch) GetSyncInterval() float64 {
	pi := w.gitRepo.Spec.PollingInterval
	if pi == nil {
		return 0
	}

	return pi.Seconds()
}

func (w *Watch) fetchBySyncInterval(ctx context.Context, ticker *time.Ticker) {
	w.log.V(1).Info("start watching latest commit", "gitrepo-name", w.gitRepo.Name)
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
	commit, err := w.fetcher.LatestCommit(ctx, &w.gitRepo, w.client)
	if err != nil {
		w.log.Error(err, "error fetching commit", "gitrepo name", w.gitRepo.Name)
		return
	}
	if w.gitRepo.Status.Commit != commit {
		w.log.Info("new commit found", "gitrepo name", w.gitRepo.Name, "commit", commit)
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var gitRepoFromCluster v1alpha1.GitRepo
			err := w.client.Get(ctx, types.NamespacedName{Name: w.gitRepo.Name, Namespace: w.gitRepo.Namespace}, &gitRepoFromCluster)
			if err != nil {
				return err
			}
			gitRepoFromCluster.Status.Commit = commit

			return w.client.Status().Update(ctx, &gitRepoFromCluster)
		}); err != nil {
			w.log.Error(err, "error updating status when a new commit was found by polling", "gitrepo", w.gitRepo)
		}
	}
}

func calculateSyncInterval(gitRepo v1alpha1.GitRepo) time.Duration {
	if gitRepo.Spec.PollingInterval != nil {
		return gitRepo.Spec.PollingInterval.Duration
	}

	return time.Duration(defaultSyncInterval) * time.Second
}
