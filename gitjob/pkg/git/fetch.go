package git

import (
	"context"

	gitjobv1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DefaultSecretName = "gitcredential" //nolint:gosec // this is a resource name
)

type Fetch struct{}

func (f *Fetch) LatestCommit(ctx context.Context, gitjob *gitjobv1.GitJob, client client.Client) (string, error) {
	secretName := DefaultSecretName
	if gitjob.Spec.Git.ClientSecretName != "" {
		secretName = gitjob.Spec.Git.ClientSecretName
	}
	var secret corev1.Secret
	err := client.Get(ctx, types.NamespacedName{
		Namespace: gitjob.Namespace,
		Name:      secretName,
	}, &secret)

	if err != nil && !errors.IsNotFound(err) {
		return "", err
	}

	branch := gitjob.Spec.Git.Branch
	if branch == "" {
		branch = "master"
	}

	git, err := newGit("", gitjob.Spec.Git.Repo, &options{
		CABundle:          gitjob.Spec.Git.Credential.CABundle,
		Credential:        &secret,
		InsecureTLSVerify: gitjob.Spec.Git.Credential.InsecureSkipTLSverify,
	})
	if err != nil {
		return "", err
	}

	return git.lsRemote(branch, gitjob.Status.Commit)
}
