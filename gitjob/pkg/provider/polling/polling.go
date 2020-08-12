package polling

import (
	"context"
	"net/http"

	"github.com/rancher/gitjob/pkg/provider"

	gitjobv1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	"github.com/rancher/gitwatcher/pkg/git"
	"github.com/rancher/wrangler/pkg/apply"
	corev1controller "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
)

var (
	T = true
)

type Polling struct {
	secretCache corev1controller.SecretCache
	apply       apply.Apply
}

func NewPolling(secrets corev1controller.SecretCache) *Polling {
	return &Polling{
		secretCache: secrets,
	}
}

func (p *Polling) Supports(obj *gitjobv1.GitJob) bool {
	return obj.Spec.Git.Provider == "polling"
}

func (p *Polling) Handle(ctx context.Context, obj *gitjobv1.GitJob) (gitjobv1.GitjobStatus, error) {
	var (
		auth git.Auth
	)

	secretName := provider.DefaultSecretName
	if obj.Spec.Git.GitSecretName != "" {
		secretName = obj.Spec.Git.GitSecretName
	}
	secret, err := p.secretCache.Get(obj.Namespace, secretName)
	if errors.IsNotFound(err) {
		secret = nil
	} else if err != nil {
		return obj.Status, err
	}

	if secret != nil {
		auth, _ = git.FromSecret(secret.Data)
	}

	branch := obj.Spec.Git.Branch
	if branch == "" {
		branch = "master"
	}

	commit, err := git.BranchCommit(ctx, obj.Spec.Git.Repo, branch, &auth)
	if err != nil {
		return obj.Status, err
	}

	obj.Status.Commit = commit
	return obj.Status, nil
}

func (p *Polling) HandleHook(ctx context.Context, req *http.Request) (int, error) {
	return 0, nil
}
