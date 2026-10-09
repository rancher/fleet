package github

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
)

type TLSConfig struct {
	CABundle           []byte
	InsecureSkipVerify bool
}

type GitHubApp struct {
	repoURL          string
	appID, installID int64
	pem              []byte
}

// NewApp creates a new GitHubApp instance with the provided app ID, installation ID,
// and private key, and returns a pointer to it.
func NewApp(repo string, appID, installID int64, pem []byte) *GitHubApp {
	return &GitHubApp{repoURL: repo, appID: appID, installID: installID, pem: pem}
}

// GetToken retrieves a GitHub App installation token using the provided app ID,
// installation ID, and private key (PEM format). It returns the token as a string
// or an error if the process fails.
func (app *GitHubApp) GetToken(ctx context.Context, tlsCfg TLSConfig) (string, error) {
	err := app.checkIfPrivateKeyIsValid()
	if err != nil {
		return "", err
	}

	tr := transportWithTLS(tlsCfg)

	itr, err := newGithubApp(tr, app.repoURL, app.appID, app.installID, app.pem)
	if err != nil {
		return "", err
	}

	token, err := itr.Token(ctx)
	if err != nil {
		return "", err
	}

	return token, nil
}

func transportWithTLS(cfg TLSConfig) http.RoundTripper {
	if len(cfg.CABundle) == 0 && !cfg.InsecureSkipVerify {
		return http.DefaultTransport
	}

	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec
	}

	if len(cfg.CABundle) > 0 {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		pool.AppendCertsFromPEM(cfg.CABundle)
		tlsConfig.RootCAs = pool
	}

	base, ok := http.DefaultTransport.(*http.Transport)
	if ok {
		base = base.Clone()
	} else {
		// Another component has replaced the global default transport: keep the
		// stdlib defaults, notably proxy settings from the environment.
		base = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
	}
	base.TLSClientConfig = tlsConfig

	return base
}

func (app *GitHubApp) checkIfPrivateKeyIsValid() error {
	blk, _ := pem.Decode(app.pem)
	if blk == nil {
		return fmt.Errorf("githubapp: pem decode failed for app %d", app.appID)
	}
	if blk.Type != "RSA PRIVATE KEY" {
		return fmt.Errorf("githubapp: unsupported key type %q for app %d", blk.Type, app.appID)
	}
	if _, err := x509.ParsePKCS1PrivateKey(blk.Bytes); err != nil {
		return fmt.Errorf("githubapp: invalid RSA key for app %d: %w", app.appID, err)
	}
	return nil
}

// newGithubApp returns a Github App Transport using a private key.
// It works around the impossibility of calling `ghinstallation.New` with a custom base URL, i.e. for repositories
// living on a `*.ghe.com` host, rather than `github.com`, as `New` calls `NewAppsTransport`, itself calling
// `NewAppsTransportFromPrivateKey`, which sets the base URL to `api.github.com`.
// To be revisited once https://github.com/bradleyfalzon/ghinstallation/issues/183 is addressed.
func newGithubApp(
	tr http.RoundTripper,
	repoURL string,
	appID, installationID int64,
	privateKey []byte,
) (*ghinstallation.Transport, error) {

	atr, err := ghinstallation.NewAppsTransport(tr, appID, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create transport for Github App: %w", err)
	}

	atr.BaseURL, err = getBaseURL(repoURL)
	if err != nil {
		return nil, fmt.Errorf("failed to extract base Github App URL from GitRepo URL: %w", err)
	}

	return ghinstallation.NewFromAppsTransport(atr, installationID), nil
}

// getBaseURL extracts the host from repoURL, and returns the corresponding base URL for Github App auth:
// `https://api.<repo_host>`.
func getBaseURL(repoURL string) (string, error) {
	url, err := url.Parse(repoURL)
	if err != nil {
		return "", err
	}

	if strings.HasPrefix(url.Host, "api.") {
		return "https://" + url.Host, nil
	}

	return "https://api." + url.Host, nil
}
