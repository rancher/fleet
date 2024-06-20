// Copyright (c) 2021-2024 SUSE LLC
package poll

import (
	"context"
	"fmt"

	"github.com/rancher/fleet/internal/cmd/controller/grutil"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/reugn/go-quartz/quartz"
	"golang.org/x/sync/semaphore"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ quartz.Job = &GitRepoPollJob{}

type GitFetcher interface {
	LatestCommit(ctx context.Context, gitrepo *v1alpha1.GitRepo, client client.Client) (string, error)
}

type GitRepoPollJob struct {
	sem     *semaphore.Weighted
	client  client.Client
	GitRepo v1alpha1.GitRepo
	fetcher GitFetcher
}

func GitRepoPollKey(gitRepo v1alpha1.GitRepo) *quartz.JobKey {
	return quartz.NewJobKeyWithGroup(gitRepo.Name, gitRepo.Namespace)
}

func NewGitRepoPollJob(c client.Client, f GitFetcher, gitRepo v1alpha1.GitRepo) *GitRepoPollJob {
	return &GitRepoPollJob{
		sem:     semaphore.NewWeighted(1),
		client:  c,
		GitRepo: gitRepo,
		fetcher: f,
	}
}

func (j *GitRepoPollJob) Execute(ctx context.Context) error {
	if !j.sem.TryAcquire(1) {
		// already running
		return nil
	}
	defer j.sem.Release(1)

	j.fetchLatestCommitAndUpdateStatus(ctx)

	return nil
}

func (j *GitRepoPollJob) Description() string {
	return j.String()
}

func (j *GitRepoPollJob) String() string {
	return fmt.Sprintf("gitrepo-%s-%s", j.GitRepo.Namespace, j.GitRepo.Name)
}

func (j *GitRepoPollJob) fetchLatestCommitAndUpdateStatus(ctx context.Context) {
	logger := ctrl.Log.WithName("git-latest-commit-poll-watch")
	commit, err := j.fetcher.LatestCommit(ctx, &j.GitRepo, j.client)
	if err != nil {
		logger.Error(err, "error fetching commit", "gitrepo", j.GitRepo)
		nsName := types.NamespacedName{Name: j.GitRepo.Name, Namespace: j.GitRepo.Namespace}
		_ = grutil.UpdateErrorStatus(ctx, j.client, nsName, j.GitRepo.Status, err)
		return
	}
	if j.GitRepo.Status.Commit != commit {
		logger.Info("new commit found", "gitrepo", j.GitRepo, "commit", commit)
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var gitRepoFromCluster v1alpha1.GitRepo
			err := j.client.Get(ctx, types.NamespacedName{Name: j.GitRepo.Name, Namespace: j.GitRepo.Namespace}, &gitRepoFromCluster)
			if err != nil {
				return err
			}
			gitRepoFromCluster.Status.Commit = commit

			return j.client.Status().Update(ctx, &gitRepoFromCluster)
		}); err != nil {
			logger.Error(err, "error updating status when a new commit was found by polling", "gitrepo", j.GitRepo)
		}
	}
}
