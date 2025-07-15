package github

import (
	"fmt"
	"reflect"
	"testing"

	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	corev1 "k8s.io/api/core/v1"
)

func TestGetGithubAppAuthFromSecret(t *testing.T) {
	validAuth := &httpgit.BasicAuth{Username: "x-access-token", Password: "token"}

	tests := []struct {
		name        string
		secret      *corev1.Secret
		stub        func(appID, instID int64, pem []byte) (*httpgit.BasicAuth, error)
		wantAuth    *httpgit.BasicAuth
		wantHasKeys bool
		wantErr     bool
	}{
		{
			name: "missing some keys (should be skipped)",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					GitHubAppAuthIDKey: []byte("123"),
				},
			},
			wantAuth:    nil,
			wantHasKeys: false,
			wantErr:     false,
		},
		{
			name: "all keys present – success path",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					GitHubAppAuthIDKey:             []byte("123"),
					GitHubAppAuthInstallationIDKey: []byte("456"),
					GitHubAppAuthPrivateKeyKey:     []byte("my-pem"),
				},
			},
			stub: func(appID, instID int64, pem []byte) (*httpgit.BasicAuth, error) {
				return &httpgit.BasicAuth{Username: "x-access-token", Password: "token"}, nil
			},
			wantAuth:    validAuth,
			wantHasKeys: true,
			wantErr:     false,
		},
		{
			name: "all keys present – GetGitHubAppAuth returns error",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					GitHubAppAuthIDKey:             []byte("123"),
					GitHubAppAuthInstallationIDKey: []byte("456"),
					GitHubAppAuthPrivateKeyKey:     []byte("my-pem"),
				},
			},
			stub: func(appID, instID int64, pem []byte) (*httpgit.BasicAuth, error) {
				return nil, fmt.Errorf("token fetch failed")
			},
			wantAuth:    nil,
			wantHasKeys: true,
			wantErr:     true,
		},
		{
			name: "non‑numeric app id",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					GitHubAppAuthIDKey:             []byte("abc"),
					GitHubAppAuthInstallationIDKey: []byte("456"),
					GitHubAppAuthPrivateKeyKey:     []byte("my-pem"),
				},
			},
			wantAuth:    nil,
			wantHasKeys: true,
			wantErr:     true,
		},
		{
			name: "non‑numeric installation id",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					GitHubAppAuthIDKey:             []byte("123"),
					GitHubAppAuthInstallationIDKey: []byte("xyz"),
					GitHubAppAuthPrivateKeyKey:     []byte("my-pem"),
				},
			},
			wantAuth:    nil,
			wantHasKeys: true,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// backup and optionally stub the package‑level helper
			orig := GetGitHubAppAuth
			if tt.stub != nil {
				GetGitHubAppAuth = tt.stub
			}
			defer func() { GetGitHubAppAuth = orig }()

			gotAuth, gotHasKeys, err := GetGithubAppAuthFromSecret(tt.secret)

			if (err != nil) != tt.wantErr {
				t.Fatalf("error mismatch: got %v, wantErr %v", err, tt.wantErr)
			}
			if gotHasKeys != tt.wantHasKeys {
				t.Fatalf("hasKeys mismatch: got %v, want %v", gotHasKeys, tt.wantHasKeys)
			}
			if !reflect.DeepEqual(gotAuth, tt.wantAuth) {
				t.Fatalf("auth mismatch: got %+v, want %+v", gotAuth, tt.wantAuth)
			}
		})
	}
}
