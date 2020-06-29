package github

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/go-github/v28/github"
	v1 "github.com/rancher/gitjobs/pkg/apis/gitops.cattle.io/v1"
	v1controller "github.com/rancher/gitjobs/pkg/generated/controllers/gitops.cattle.io/v1"
	"github.com/rancher/gitjobs/pkg/provider"
	"github.com/rancher/gitwatcher/pkg/git"
	corev1controller "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/kv"
	"k8s.io/apimachinery/pkg/api/errors"
)

const (
	GitWebHookParam = "gitjobId"
)

const (
	statusOpened = "opened"
	statusSynced = "synchronize"
)

type GitHub struct {
	Gitjobs     v1controller.GitJobController
	secretCache corev1controller.SecretCache
}

func NewGitHub(gitjob v1controller.GitJobController) *GitHub {
	return &GitHub{
		Gitjobs: gitjob,
	}
}

func (w *GitHub) Supports(obj *v1.GitJob) bool {
	if strings.EqualFold(obj.Spec.Git.Provider, "github") {
		return true
	}

	return false
}

func (w *GitHub) Handle(ctx context.Context, obj *v1.GitJob) (v1.GitJobStatus, error) {
	if obj.Status.GithubMeta != nil && obj.Status.GithubMeta.Initialized {
		return obj.Status, nil
	}

	var (
		auth git.Auth
	)

	secretName := provider.DefaultSecretName
	if obj.Spec.Git.GitSecretName != "" {
		secretName = obj.Spec.Git.GitSecretName
	}
	secret, err := w.secretCache.Get(obj.Namespace, secretName)
	if errors.IsNotFound(err) {
		secret = nil
	} else if err != nil {
		return obj.Status, err
	}

	if secret != nil {
		auth, _ = git.FromSecret(secret.Data)
	}

	commit, err := git.BranchCommit(ctx, obj.Spec.Git.Repo, obj.Spec.Git.Branch, &auth)
	if err != nil {
		return obj.Status, err
	}

	obj.Status.Commit = commit
	return obj.Status, nil
}

func (w *GitHub) HandleHook(ctx context.Context, req *http.Request) (int, error) {
	receiverID := req.URL.Query().Get(GitWebHookParam)
	if receiverID == "" {
		return 0, nil
	}

	ns, name := kv.Split(receiverID, ":")
	gitjob, err := w.Gitjobs.Cache().Get(ns, name)
	if err != nil {
		return http.StatusInternalServerError, err
	}

	payload, err := github.ValidatePayload(req, []byte(gitjob.Spec.Git.Github.Token))
	if err != nil {
		return http.StatusInternalServerError, err
	}

	event, err := github.ParseWebHook(github.WebHookType(req), payload)
	if err != nil {
		return http.StatusInternalServerError, err
	}

	return w.handleEvent(ctx, event, gitjob)
}

func (w *GitHub) handleEvent(ctx context.Context, event interface{}, gitjob *v1.GitJob) (int, error) {
	switch event.(type) {
	case *github.PushEvent:
		parsed := event.(*github.PushEvent)

		gitjob.Status.Commit = safeString(parsed.GetHeadCommit().ID)
		gitjob.Status.GithubMeta = &v1.GithubMeta{
			Event: "push",
		}
	case *github.PullRequestEvent:
		parsed := event.(*github.PullRequestEvent)

		if parsed.Action != nil && (*parsed.Action == statusOpened || *parsed.Action == statusSynced) {
			gitjob.Status.Commit = safeString(parsed.PullRequest.Head.SHA)
			gitjob.Status.GithubMeta = &v1.GithubMeta{
				Event: "pull-request",
			}
		}
	}
	if _, err := w.Gitjobs.UpdateStatus(gitjob); err != nil {
		return http.StatusConflict, err
	}
	return http.StatusOK, nil
}

func safeString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
