package git

import (
	"errors"

	"github.com/go-git/go-git/v5/plumbing/transport"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	giturls "github.com/rancher/fleet/pkg/git-urls"
	corev1 "k8s.io/api/core/v1"

	fleetgithub "github.com/rancher/fleet/internal/github"
	"github.com/rancher/fleet/pkg/netx"
	fleetssh "github.com/rancher/fleet/internal/ssh"
)

var GitHubAppGetter fleetgithub.AppAuthGetter = fleetgithub.DefaultAppAuthGetter{}

// GetAuthFromSecret returns the AuthMethod calculated from the given secret, setting known hosts if needed.
// Known hosts are sourced from the creds, if provided there. Otherwise, they will be sourced from the provided
// knownHosts if non-empty.
// The credentials secret is expected to be either basic-auth or ssh-auth (with extra known_hosts data option)
func GetAuthFromSecret(url string, creds *corev1.Secret, knownHosts string, tlsCfg netx.Config) (transport.AuthMethod, error) {
	if creds == nil {
		// no auth information was provided
		return nil, nil
	}

	switch creds.Type {
	case corev1.SecretTypeBasicAuth:
		username, password := creds.Data[corev1.BasicAuthUsernameKey], creds.Data[corev1.BasicAuthPasswordKey]
		if len(username) == 0 {
			if len(password) == 0 {
				return nil, nil
			}

			return &httpgit.BasicAuth{
				Username: string(password),
			}, nil
		}
		return &httpgit.BasicAuth{
			Username: string(username),
			Password: string(password),
		}, nil
	case corev1.SecretTypeSSHAuth:
		gitURL, err := giturls.Parse(url)
		if err != nil {
			return nil, err
		}
		username := "git"
		if gitURL.User != nil && gitURL.User.Username() != "" {
			username = gitURL.User.Username()
		}
		// Prefer known_hosts from the secret; fall back to the cluster-wide value.
		knownHostsData := creds.Data["known_hosts"]
		if len(knownHostsData) == 0 {
			knownHostsData = []byte(knownHosts)
		}
		auth, err := fleetssh.NewSSHPublicKeys(username, creds.Data[corev1.SSHAuthPrivateKey], knownHostsData)
		if err != nil {
			return nil, err
		}
		return auth, nil
	default:
		auth, err := fleetgithub.GetGithubAppAuthFromSecret(url, creds, GitHubAppGetter, tlsCfg)
		if err != nil {
			if errors.Is(err, fleetgithub.ErrNotGithubAppSecret) {
				return nil, nil
			}
			return nil, err
		}
		return auth, nil
	}
}
