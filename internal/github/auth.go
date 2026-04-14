package github

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	corev1 "k8s.io/api/core/v1"
)

const (
	GitHubAppAuthInstallationIDKey = "github_app_installation_id"
	GitHubAppAuthIDKey             = "github_app_id"
	GitHubAppAuthPrivateKeyKey     = "github_app_private_key"
)

var ErrNotGithubAppSecret = errors.New("not a GitHub App secret")

type AppAuthGetter interface {
	Get(repo string, appID, insID int64, pem []byte) (*httpgit.BasicAuth, error)
}

type DefaultAppAuthGetter struct{}

func (DefaultAppAuthGetter) Get(repo string, appID, insID int64, pem []byte) (*httpgit.BasicAuth, error) {
	tok, err := NewApp(repo, appID, insID, pem).GetToken(context.Background())
	if err != nil {
		return nil, fmt.Errorf("could not authenticate as GitHub App installation: %w", err)
	}
	// See https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-as-a-github-app-installation#about-authentication-as-a-github-app-installation for reference
	return &httpgit.BasicAuth{
		Username: "x-access-token",
		Password: tok,
	}, nil
}

func GetGithubAppAuthFromSecret(repo string, creds *corev1.Secret, getter AppAuthGetter) (*httpgit.BasicAuth, error) {
	idBytes, okID := creds.Data[GitHubAppAuthIDKey]
	insBytes, okIns := creds.Data[GitHubAppAuthInstallationIDKey]
	pemBytes, okPem := creds.Data[GitHubAppAuthPrivateKeyKey]
	if !okID || !okIns || !okPem {
		return nil, ErrNotGithubAppSecret
	}

	appID, err := strconv.ParseInt(string(idBytes), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("github-app id is not numeric: %w", err)
	}
	insID, err := strconv.ParseInt(string(insBytes), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("github-app installation id is not numeric: %w", err)
	}

	auth, err := getter.Get(repo, appID, insID, pemBytes)
	if err != nil {
		return nil, err
	}
	return auth, nil
}
