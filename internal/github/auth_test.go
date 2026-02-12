package github

import (
	"fmt"
	"reflect"
	"testing"

	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	corev1 "k8s.io/api/core/v1"
)

type fakeGetter struct {
	auth *httpgit.BasicAuth
	err  error
}

func (f fakeGetter) Get(_ string, appID, instID int64, pem []byte) (*httpgit.BasicAuth, error) {
	return f.auth, f.err
}

func TestGetGithubAppAuthFromSecret(t *testing.T) {
	validAuth := &httpgit.BasicAuth{Username: "x-access-token", Password: "token"}

	tests := []struct {
		name     string
		secret   *corev1.Secret
		getter   AppAuthGetter
		wantAuth *httpgit.BasicAuth
		wantErr  bool
	}{
		{
			name: "missing some keys",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					GitHubAppAuthIDKey: []byte("123"),
				},
			},
			getter:   fakeGetter{err: fmt.Errorf("not a GitHub App secret")},
			wantAuth: nil,
			wantErr:  true,
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
			getter:   fakeGetter{auth: validAuth},
			wantAuth: validAuth,
			wantErr:  false,
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
			getter:   fakeGetter{err: fmt.Errorf("token fetch failed")},
			wantAuth: nil,
			wantErr:  true,
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
			getter:   fakeGetter{}, // never called
			wantAuth: nil,
			wantErr:  true,
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
			getter:   fakeGetter{}, // never called
			wantAuth: nil,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAuth, err := GetGithubAppAuthFromSecret("https://github.com/foo/bar", tt.secret, tt.getter)

			if (err != nil) != tt.wantErr {
				t.Fatalf("error mismatch: got %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(gotAuth, tt.wantAuth) {
				t.Fatalf("auth mismatch: got %+v, want %+v", gotAuth, tt.wantAuth)
			}
		})
	}
}
