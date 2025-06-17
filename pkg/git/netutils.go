package git

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	gossh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	giturls "github.com/rancher/fleet/pkg/git-urls"
	"github.com/rancher/fleet/pkg/githubapp"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"

	fleetssh "github.com/rancher/fleet/internal/ssh"
)

const (
	GitHubAppAuthInstallationIDKey = "github_app_installation_id"
	GitHubAppAuthIDKey             = "github_app_id"
	GitHubAppAuthPrivateKeyKey     = "github_app_private_key"
)

var (
	GetGitHubAppAuth = func(appID, insID int64, pem []byte) (*httpgit.BasicAuth, error) {
		tok, err := githubapp.NewApp(appID, insID, pem).GetToken(context.Background())
		if err != nil {
			return nil, err
		}
		return &httpgit.BasicAuth{
			Username: "x-access-token",
			Password: tok,
		}, nil
	}
)

// GetAuthFromSecret returns the AuthMethod calculated from the given secret, setting known hosts if needed.
// Known hosts are sourced from the creds, if provided there. Otherwise, they will be sourced from the provided
// knownHosts if non-empty.
// The credentials secret is expected to be either basic-auth or ssh-auth (with extra known_hosts data option)
func GetAuthFromSecret(url string, creds *corev1.Secret, knownHosts string) (transport.AuthMethod, error) {
	if creds == nil {
		// no auth information was provided
		return nil, nil
	}

	switch creds.Type {
	case corev1.SecretTypeBasicAuth:
		username, password := creds.Data[corev1.BasicAuthUsernameKey], creds.Data[corev1.BasicAuthPasswordKey]
		if len(password) == 0 && len(username) == 0 {
			return nil, nil
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
		auth, err := gossh.NewPublicKeys(gitURL.User.Username(), creds.Data[corev1.SSHAuthPrivateKey], "")
		if err != nil {
			return nil, err
		}
		if creds.Data["known_hosts"] != nil {
			auth.HostKeyCallback, err = fleetssh.CreateKnownHostsCallBack(creds.Data["known_hosts"])
			if err != nil {
				return nil, err
			}
		} else if len(knownHosts) > 0 {
			auth.HostKeyCallback, err = fleetssh.CreateKnownHostsCallBack([]byte(knownHosts))
			if err != nil {
				return nil, err
			}
		} else {
			//nolint G106: Use of ssh InsecureIgnoreHostKey should be audited
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
		}
		return auth, nil
	default:
		if auth, keysArePresent, err := GetGithubAppAuthFromSecret(creds); keysArePresent {
			if err != nil {
				return nil, err
			}
			return auth, nil
		} else if err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// GetHTTPClientFromSecret returns a HTTP client filled from the information in the given secret
// and optional CABundle and insecureTLSVerify
func GetHTTPClientFromSecret(creds *corev1.Secret, CABundle []byte, insecureTLSVerify bool, timeout time.Duration) (*http.Client, error) {
	var (
		username  string
		password  string
		tlsConfig tls.Config
	)

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
			tlsConfig.Certificates = append(tlsConfig.Certificates, cert)
		}
	}

	if len(CABundle) > 0 {
		cert, err := x509.ParseCertificate(CABundle)
		if err != nil {
			return nil, err
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		pool.AddCert(cert)
		tlsConfig.RootCAs = pool
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tlsConfig
	transport.TLSClientConfig.InsecureSkipVerify = insecureTLSVerify

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

// GetGithubAppAuthFromSecret returns:
//   - (auth, true,  nil) – the secret **has** all 3 GitHub-App keys and we
//     successfully fetched a token.
//   - (nil,      false, nil) – the three keys are **not** present (caller should
//     keep looking for other credential styles).
//   - (nil,      true,  err) – keys were present but something failed (bad IDs,
//     PEM write error, network error, …).
func GetGithubAppAuthFromSecret(creds *corev1.Secret) (*httpgit.BasicAuth, bool, error) {
	idBytes, okID := creds.Data[GitHubAppAuthIDKey]
	insBytes, okIns := creds.Data[GitHubAppAuthInstallationIDKey]
	pemBytes, okPem := creds.Data[GitHubAppAuthPrivateKeyKey]
	if !(okID && okIns && okPem) {
		return nil, false, nil
	}

	appID, err := strconv.ParseInt(string(idBytes), 10, 64)
	if err != nil {
		return nil, true, fmt.Errorf("github-app id is not numeric: %w", err)
	}
	insID, err := strconv.ParseInt(string(insBytes), 10, 64)
	if err != nil {
		return nil, true, fmt.Errorf("github-app installation id is not numeric: %w", err)
	}

	auth, err := GetGitHubAppAuth(appID, insID, pemBytes)
	if err != nil {
		return nil, true, err
	}
	return auth, true, nil
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
