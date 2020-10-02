package polling

import (
	"context"
	"net/http"

	gitjobv1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	v1controller "github.com/rancher/gitjob/pkg/generated/controllers/gitjob.cattle.io/v1"
	"github.com/rancher/gitjob/pkg/provider"
	"github.com/rancher/gitjob/pkg/types"
	"github.com/rancher/wrangler/pkg/apply"
	corev1controller "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/git"
	"github.com/rancher/wrangler/pkg/kstatus"
	"k8s.io/apimachinery/pkg/api/errors"
)

var (
	T = true
)

type Polling struct {
	secretCache corev1controller.SecretCache
	gitjobs     v1controller.GitJobController
	apply       apply.Apply
}

func NewPolling(rContext *types.Context) *Polling {
	return &Polling{
		secretCache: rContext.Core.Core().V1().Secret().Cache(),
		gitjobs:     rContext.Gitjob.Gitjob().V1().GitJob(),
	}
}

func (p *Polling) Supports(obj *gitjobv1.GitJob) bool {
	return obj.Spec.Git.Provider == "polling"
}

func (p *Polling) Handle(ctx context.Context, obj *gitjobv1.GitJob) (gitjobv1.GitJobStatus, error) {
	newObj, err := p.innerHandle(ctx, obj)
	if err != nil {
		kstatus.SetError(newObj, err.Error())
	} else if !kstatus.Stalled.IsTrue(newObj) {
		kstatus.SetActive(newObj)
	}
	return newObj.Status, err
}

func (p *Polling) innerHandle(ctx context.Context, obj *gitjobv1.GitJob) (*gitjobv1.GitJob, error) {
	secretName := provider.DefaultSecretName
	if obj.Spec.Git.ClientSecretName != "" {
		secretName = obj.Spec.Git.ClientSecretName
	}
	secret, err := p.secretCache.Get(obj.Namespace, secretName)
	if errors.IsNotFound(err) {
		secret = nil
	} else if err != nil {
		return obj, err
	}

	branch := obj.Spec.Git.Branch
	if branch == "" {
		branch = "master"
	}

	git, err := git.NewGit("", obj.Spec.Git.Repo, &git.Options{
		CABundle:          obj.Spec.Git.Credential.CABundle,
		Credential:        secret,
		InsecureTLSVerify: obj.Spec.Git.Credential.InsecureSkipTLSverify,
	})
	if err != nil {
		return obj, err
	}

	commit, err := git.LsRemote(branch, obj.Status.Commit)
	if err != nil {
		return obj, err
	}

	obj.Status.Commit = commit
	return obj, err
}

func (p *Polling) HandleHook(ctx context.Context, req *http.Request) (int, error) {
	return 0, nil
}
