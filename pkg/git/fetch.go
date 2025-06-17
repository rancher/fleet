package git

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/ssh"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/cert"
	"github.com/rancher/fleet/pkg/githubapp"

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
	KnownHosts    KnownHostsGetter
	TokenProvider func(ctx context.Context) (string, error)
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

	// Fall back to Rancher-configured CA bundles if no CA bundle is specified in the GitRepo
	cabundle := gitrepo.Spec.CABundle
	if len(cabundle) == 0 {
		cab, err := cert.GetRancherCABundle(ctx, client)
		if err != nil {
			return "", err
		}

		cabundle = cab
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

	if secret.Data != nil && f.KnownHosts != nil && !f.KnownHosts.IsStrict() {
		// This prevents errors about keys being mismatch or not found when host key checks are disabled.
		secret.Data["known_hosts"] = nil
	}

	var token string
	if f.TokenProvider != nil {
		t, err := f.TokenProvider(ctx)
		if err != nil {
			return "", err
		}
		token = t
	}

	if token == "" && secret.Data != nil {
		_, hasID := secret.Data["github_app_id"]
		_, hasIns := secret.Data["github_app_installation_id"]
		pem, hasKey := secret.Data["github_app_private_key"]
		if hasID && hasIns && hasKey {
			appID, _ := strconv.ParseInt(string(secret.Data["github_app_id"]), 10, 64)
			insID, _ := strconv.ParseInt(string(secret.Data["github_app_installation_id"]), 10, 64)

			// write the PEM to a temp file owned by the controller
			tmp := "/tmp/fleet_app_key.pem"
			_ = os.WriteFile(tmp, pem, 0600)

			prov := githubapp.New(appID, insID, tmp)
			tkn, err := prov.GetToken(ctx)
			if err != nil {
				return "", fmt.Errorf("github-app token for %s/%s: %w",
					gitrepo.Namespace, gitrepo.Name, err)
			}
			token = tkn
		}
	}

	r, err := NewRemote(gitrepo.Spec.Repo, &options{
		CABundle:          cabundle,
		Credential:        &secret,
		Token:             token,
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
