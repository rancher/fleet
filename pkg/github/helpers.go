package github

import (
	corev1 "k8s.io/api/core/v1"
)

const (
	GithubAppIDKey             = "github_app_id"
	GithubAppInstallationIDKey = "github_app_installation_id"
	GithubAppPrivateKeyKey     = "github_app_private_key"
)

// HasGitHubAppKeys checks if the provided Kubernetes secret contains the necessary keys
// for a GitHub App: app ID, installation ID, and private key.
func HasGitHubAppKeys(secret *corev1.Secret) bool {
	if secret == nil {
		return false
	}

	_, hasID := secret.Data[GithubAppIDKey]
	_, hasInstallationID := secret.Data[GithubAppInstallationIDKey]
	_, hasPrivateKey := secret.Data[GithubAppPrivateKeyKey]

	return hasID && hasInstallationID && hasPrivateKey
}
