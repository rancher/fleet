package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/go-github/v28/github"
	"github.com/google/uuid"
	v1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	v1controller "github.com/rancher/gitjob/pkg/generated/controllers/gitjob.cattle.io/v1"
	"github.com/rancher/gitjob/pkg/types"
	corev1controller "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/kstatus"
	"github.com/rancher/wrangler/pkg/kv"
	"golang.org/x/oauth2"
	corev1 "k8s.io/api/core/v1"
)

var (
	client = http.DefaultClient
)

const (
	GitWebHookParam = "gitjobId"
)

const (
	statusOpened = "opened"
	statusSynced = "synchronize"

	kubeSystem          = "kube-system"
	githubConfigmapName = "github-setting"
	githubSecretName    = "SecretName"
	githubWebhookURL    = "WebhookURL"
)

type GitHub struct {
	gitjobs     v1controller.GitJobController
	configmaps  corev1controller.ConfigMapCache
	secretCache corev1controller.SecretCache
	client      *github.Client
	namespace   string
}

func NewGitHub(rContext *types.Context) *GitHub {
	return &GitHub{
		namespace:   rContext.Namespace,
		configmaps:  rContext.Core.Core().V1().ConfigMap().Cache(),
		secretCache: rContext.Core.Core().V1().Secret().Cache(),
		gitjobs:     rContext.Gitjob.Gitjob().V1().GitJob(),
	}
}

func (w *GitHub) Supports(obj *v1.GitJob) bool {
	if strings.EqualFold(obj.Spec.Git.Provider, "github") {
		return true
	}

	return false
}

func (w *GitHub) Handle(ctx context.Context, obj *v1.GitJob) (v1.GitJobStatus, error) {
	newObj, err := w.innerHandle(ctx, obj)
	if err != nil {
		kstatus.SetError(newObj, err.Error())
	} else if !kstatus.Stalled.IsTrue(newObj) {
		kstatus.SetActive(newObj)
	}
	return newObj.Status, err
}

func (w *GitHub) innerHandle(ctx context.Context, obj *v1.GitJob) (*v1.GitJob, error) {
	cm, secret, err := w.getGithubSettingAndSecret()
	if err != nil {
		// if no github setting and token is found, skip creating webhook
		return obj, nil
	}

	if obj.Status.HookID != "" {
		return obj, nil
	}

	if w.client == nil {
		token := secret.Data["token"]
		ts := oauth2.StaticTokenSource(
			&oauth2.Token{AccessToken: string(token)},
		)
		subCtx := context.WithValue(ctx, oauth2.HTTPClient, client)
		tc := oauth2.NewClient(subCtx, ts)
		w.client = github.NewClient(tc)
	}

	owner, repo, err := getOwnerAndRepo(obj.Spec.Git.Repo)
	if err != nil {
		return obj, err
	}

	obj.Status.ValidationToken = uuid.New().String()
	hook, _, err := w.client.Repositories.CreateHook(ctx, owner, repo, &github.Hook{
		Events: []string{"push"},
		Config: map[string]interface{}{
			"url":    hookURL(obj, cm),
			"secret": obj.Status.ValidationToken,
		},
	})
	if err != nil {
		return obj, fmt.Errorf("failed to create hook for %s/%s, error: %v", owner, repo, err)
	}

	obj.Status.HookID = strconv.Itoa(int(*hook.ID))
	return obj, nil
}

func (w *GitHub) HandleHook(ctx context.Context, req *http.Request) (int, error) {
	receiverID := req.URL.Query().Get(GitWebHookParam)
	if receiverID == "" {
		return 0, nil
	}

	ns, name := kv.Split(receiverID, ":")
	gitjob, err := w.gitjobs.Cache().Get(ns, name)
	if err != nil {
		return http.StatusInternalServerError, err
	}

	token := gitjob.Spec.Git.Github.Token
	if token == "" {
		token = gitjob.Status.ValidationToken
	}
	payload, err := github.ValidatePayload(req, []byte(token))
	if err != nil {
		return http.StatusInternalServerError, err
	}

	event, err := github.ParseWebHook(github.WebHookType(req), payload)
	if err != nil {
		return http.StatusInternalServerError, err
	}

	return w.handleEvent(ctx, event, gitjob)
}

func (w *GitHub) getGithubSettingAndSecret() (*corev1.ConfigMap, *corev1.Secret, error) {
	configmap, err := w.configmaps.Get(kubeSystem, githubConfigmapName)
	if err != nil {
		return nil, nil, err
	}

	secret, err := w.secretCache.Get(kubeSystem, configmap.Data[githubSecretName])
	if err != nil {
		return nil, nil, err
	}

	return configmap, secret, nil
}

func (w *GitHub) handleEvent(ctx context.Context, event interface{}, gitjob *v1.GitJob) (int, error) {
	switch event.(type) {
	case *github.PushEvent:
		parsed := event.(*github.PushEvent)

		gitjob.Status.Commit = safeString(parsed.GetHeadCommit().ID)
		gitjob.Status.GithubMeta = v1.GithubMeta{
			Event: "push",
		}
	case *github.PullRequestEvent:
		parsed := event.(*github.PullRequestEvent)

		if parsed.Action != nil && (*parsed.Action == statusOpened || *parsed.Action == statusSynced) {
			gitjob.Status.Commit = safeString(parsed.PullRequest.Head.SHA)
			gitjob.Status.GithubMeta = v1.GithubMeta{
				Event: "pull-request",
			}
		}
	}
	if _, err := w.gitjobs.UpdateStatus(gitjob); err != nil {
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

func getOwnerAndRepo(repoURL string) (string, string, error) {
	u, err := url.Parse(repoURL)
	if err != nil {
		return "", "", err
	}
	repo := strings.TrimPrefix(u.Path, "/")
	repo = strings.TrimSuffix(repo, ".git")
	owner, repo := kv.Split(repo, "/")
	return owner, repo, nil
}

func hookURL(obj *v1.GitJob, cm *corev1.ConfigMap) string {
	return fmt.Sprintf("%s?%s=%s:%s", cm.Data[githubWebhookURL], GitWebHookParam, obj.Namespace, obj.Name)
}
