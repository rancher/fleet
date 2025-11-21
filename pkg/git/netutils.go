package git

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	gossh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	giturls "github.com/rancher/fleet/pkg/git-urls"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"

	fleetgithub "github.com/rancher/fleet/internal/github"
	fleetssh "github.com/rancher/fleet/internal/ssh"
)

var GitHubAppGetter fleetgithub.AppAuthGetter = fleetgithub.DefaultAppAuthGetter{}

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
			//nolint:gosec // G106: Use of ssh InsecureIgnoreHostKey should be audited
			auth.HostKeyCallback = ssh.InsecureIgnoreHostKey()
		}
		return auth, nil
	default:
		auth, err := fleetgithub.GetGithubAppAuthFromSecret(creds, GitHubAppGetter)
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

type basicRoundTripper struct {
	username string
	password string
	next     http.RoundTripper
}

func (b *basicRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	request.SetBasicAuth(b.username, b.password)
	return b.next.RoundTrip(request)
}
