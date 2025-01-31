package git

import (
	"context"

	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/ssh"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type KnownHostsGetter interface {
	GetWithSecret(ctx context.Context, c client.Client, secret *corev1.Secret) (string, error)
	IsStrict() bool
}

type Fetch struct {
	KnownHosts KnownHostsGetter
}

func NewFetch() *Fetch {
	return &Fetch{}
}

func (f *Fetch) LatestCommit(ctx context.Context, gitrepo *v1alpha1.GitRepo, client client.Client) (string, error) {
	secretName := config.DefaultGitCredentialsSecretName
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

	var knownHosts string
	if f.KnownHosts != nil && f.KnownHosts.IsStrict() && ssh.Is(gitrepo.Spec.Repo) {
		kh, err := f.KnownHosts.GetWithSecret(ctx, client, &secret)
		if err != nil {
			return "", err
		}

		// known_hosts data may come from sources other than the secret, such as a config map.
		knownHosts = kh
	}

	if f.KnownHosts != nil && !f.KnownHosts.IsStrict() {
		// This prevents errors about keys being mismatch or not found when host key checks are disabled.
		secret.Data["known_hosts"] = nil
	}

	r, err := NewRemote(gitrepo.Spec.Repo, &options{
		CABundle:          gitrepo.Spec.CABundle,
		Credential:        &secret,
		InsecureTLSVerify: gitrepo.Spec.InsecureSkipTLSverify,
		KnownHosts:        knownHosts,
		Timeout:           config.Get().GitClientTimeout.Duration,
		log:               log.FromContext(ctx),
	})
	if err != nil {
		return "", err
	}

	if gitrepo.Spec.Revision != "" {
		return r.RevisionCommit(gitrepo.Spec.Revision)
	}
	return r.LatestBranchCommit(branch)
}
