package git

import (
	"context"

	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DefaultSecretName = "gitcredential" //nolint:gosec // this is a resource name
)

type Fetch struct{}

func (f *Fetch) LatestCommit(ctx context.Context, gitrepo *v1alpha1.GitRepo, client client.Client) (string, error) {
	secretName := DefaultSecretName
	if gitrepo.Spec.ClientSecretName != "" {
		secretName = gitrepo.Spec.ClientSecretName
	}
	var secret corev1.Secret
	err := client.Get(ctx, types.NamespacedName{
		Namespace: gitrepo.Namespace,
		Name:      secretName,
	}, &secret)

	if err != nil && !errors.IsNotFound(err) {
		return "", err
	}

	branch := gitrepo.Spec.Branch
	if branch == "" {
		branch = "master"
	}

	git, err := newGit("", gitrepo.Spec.Repo, &options{
		CABundle:          gitrepo.Spec.CABundle,
		Credential:        &secret,
		InsecureTLSVerify: gitrepo.Spec.InsecureSkipTLSverify,
	})
	if err != nil {
		return "", err
	}

	return git.lsRemote(branch, gitrepo.Status.Commit)
}
