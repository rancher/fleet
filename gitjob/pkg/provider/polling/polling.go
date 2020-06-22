package polling

import (
	"context"

	gitopsv1 "github.com/rancher/gitjobs/pkg/apis/gitops.cattle.io/v1"
	"github.com/rancher/gitwatcher/pkg/git"
	corev1controller "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/apply"
	"k8s.io/apimachinery/pkg/api/errors"
)

var (
	T = true
)

const (
	defaultSecretName = "gitcredential"
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

func (w *Polling) Supports(obj *gitopsv1.GitJob) bool {
	return obj.Spec.Git.Provider == "polling"
}

func (w *Polling) Handle(ctx context.Context, obj *gitopsv1.GitJob) (gitopsv1.GitJobStatus, error) {
	var (
		auth git.Auth
	)

	secretName := defaultSecretName
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
