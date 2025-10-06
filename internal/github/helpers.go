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

	id, hasID := secret.Data[GithubAppIDKey]
	installationID, hasInstallationID := secret.Data[GithubAppInstallationIDKey]
	privateKey, hasPrivateKey := secret.Data[GithubAppPrivateKeyKey]

	return hasID && len(id) > 0 &&
		hasInstallationID && len(installationID) > 0 &&
		hasPrivateKey && len(privateKey) > 0
}
