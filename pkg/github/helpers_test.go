package github

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestHasGitHubAppKeys(t *testing.T) {
	tests := []struct {
		name   string
		secret *corev1.Secret
		want   bool
	}{
		{
			name:   "nil secret",
			secret: nil,
			want:   false,
		},
		{
			name:   "empty secret",
			secret: &corev1.Secret{},
			want:   false,
		},
		{
			name: "only app id",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					GithubAppIDKey: []byte("1"),
				},
			},
			want: false,
		},
		{
			name: "missing installation id",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					GithubAppIDKey:         []byte("1"),
					GithubAppPrivateKeyKey: []byte("priv"),
				},
			},
			want: false,
		},
		{
			name: "all keys present",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					GithubAppIDKey:             []byte("1"),
					GithubAppInstallationIDKey: []byte("123"),
					GithubAppPrivateKeyKey:     []byte("priv"),
				},
			},
			want: true,
		},
		{
			name: "all keys present but app id empty",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					GithubAppIDKey:             []byte(""),
					GithubAppInstallationIDKey: []byte("123"),
					GithubAppPrivateKeyKey:     []byte("priv"),
				},
			},
			want: false,
		},
		{
			name: "all keys present but installation id empty",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					GithubAppIDKey:             []byte("1"),
					GithubAppInstallationIDKey: []byte(""),
					GithubAppPrivateKeyKey:     []byte("priv"),
				},
			},
			want: false,
		},
		{
			name: "all keys present but private key empty",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					GithubAppIDKey:             []byte("1"),
					GithubAppInstallationIDKey: []byte("123"),
					GithubAppPrivateKeyKey:     []byte(""),
				},
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := HasGitHubAppKeys(tc.secret)
			if got != tc.want {
				t.Errorf("HasGitHubAppKeys() = %v, want %v", got, tc.want)
			}
		})
	}
}
