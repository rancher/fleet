package git

import (
	"crypto/tls"
	"errors"
	"net/http"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	fleetgithub "github.com/rancher/fleet/internal/github"
	"github.com/rancher/fleet/internal/netx"
	fleetssh "github.com/rancher/fleet/internal/ssh"
	giturls "github.com/rancher/fleet/pkg/git-urls"
	corev1 "k8s.io/api/core/v1"
)

var GitHubAppGetter fleetgithub.AppAuthGetter = fleetgithub.DefaultAppAuthGetter{}

// GetAuthFromSecret returns the AuthMethod calculated from the given secret, setting known hosts if needed.
// Known hosts are sourced from the creds, if provided there. Otherwise, they will be sourced from the provided
// knownHosts if non-empty.
// The credentials secret is expected to be either basic-auth or ssh-auth (with extra known_hosts data option)
func GetAuthFromSecret(url string, creds *corev1.Secret, knownHosts string, tlsCfg netx.TLSConfig) (transport.AuthMethod, error) {
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

// GetHTTPClientFromSecret returns a HTTP client filled from the information in the given secret
// and the TLS settings in tlsCfg
func GetHTTPClientFromSecret(creds *corev1.Secret, bundleCA []byte, insecureTLSVerify bool, timeout time.Duration) (*http.Client, error) {
	var (
		username string
		password string
	)
	tlsCfg := netx.TLSConfig{
		CABundle:           bundleCA,
		InsecureSkipVerify: insecureTLSVerify,
	}

	if creds != nil {
		switch creds.Type {
		case corev1.SecretTypeBasicAuth:
			username = string(creds.Data[corev1.BasicAuthUsernameKey])
			password = string(creds.Data[corev1.BasicAuthPasswordKey])
		case corev1.SecretTypeTLS:
			cert, err := tls.X509KeyPair(creds.Data[corev1.TLSCertKey], creds.Data[corev1.TLSPrivateKeyKey])
			if err != nil {
				return nil, err
			}
			tlsCfg.Certificates = append(tlsCfg.Certificates, cert)
		}
	}

	transport, err := netx.NewTransport(tlsCfg)
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
	if username != "" || password != "" {
		client.Transport = &basicRoundTripper{
			username: username,
			password: password,
			next:     client.Transport,
		}
	}

	return client, nil
}

type basicRoundTripper struct {
	username string
	password string
	next     http.RoundTripper
}

func (b *basicRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	request.SetBasicAuth(b.username, b.password)
	return b.next.RoundTrip(request)
}
