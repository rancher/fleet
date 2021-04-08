package git

import (
	gitjobv1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	"github.com/rancher/wrangler/pkg/git"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
)

const (
	DefaultSecretName = "gitcredential"
)

type SecretGetter interface {
	Get(string, string) (*v1.Secret, error)
}

func LatestCommit(gitjob *gitjobv1.GitJob, secretGetter SecretGetter) (string, error) {
	secretName := DefaultSecretName
	if gitjob.Spec.Git.ClientSecretName != "" {
		secretName = gitjob.Spec.Git.ClientSecretName
	}
	secret, err := secretGetter.Get(gitjob.Namespace, secretName)
	if errors.IsNotFound(err) {
		secret = nil
	} else if err != nil {
		return "", err
	}

	branch := gitjob.Spec.Git.Branch
	if branch == "" {
		branch = "master"
	}

	git, err := git.NewGit("", gitjob.Spec.Git.Repo, &git.Options{
		CABundle:          gitjob.Spec.Git.Credential.CABundle,
		Credential:        secret,
		InsecureTLSVerify: gitjob.Spec.Git.Credential.InsecureSkipTLSverify,
	})
	if err != nil {
		return "", err
	}

	return git.LsRemote(branch, gitjob.Status.Commit)
}
