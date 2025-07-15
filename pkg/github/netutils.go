package github

import (
	"context"
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

var (
	GetGitHubAppAuth = func(appID, insID int64, pem []byte) (*httpgit.BasicAuth, error) {
		tok, err := NewApp(appID, insID, pem).GetToken(context.Background())
		if err != nil {
			return nil, err
		}
		return &httpgit.BasicAuth{
			Username: "x-access-token",
			Password: tok,
		}, nil
	}
)

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
