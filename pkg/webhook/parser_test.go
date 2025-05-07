package webhook

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/go-playground/webhooks/v6/azuredevops"
	"github.com/go-playground/webhooks/v6/bitbucket"
	bitbucketserver "github.com/go-playground/webhooks/v6/bitbucket-server"
	"github.com/go-playground/webhooks/v6/github"
	"github.com/go-playground/webhooks/v6/gitlab"
	gogsclient "github.com/gogits/go-gogs-client"
	"gotest.tools/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/kubectl/pkg/scheme"
)

func TestParseGogs(t *testing.T) {
	tests := map[string]struct {
		secretData map[string][]byte
		body       []byte
		headers    map[string]string
		wantErr    bool
		wantErrMsg string
		wantEvent  interface{}
	}{
		"valid-gogs-push-event-no-secret": {
			secretData: nil,
			body: []byte(`{
				"ref": "refs/heads/main",
				"after": "af69d162de5a276abc86e0686b2b44033cd3f442",
				"repository": {
					"html_url": "https://gogs.example.com/example/repo"
				}
			}`),
			headers: map[string]string{
				"X-Gogs-Event": "push",
			},
			wantErr: false,
			wantEvent: gogsclient.PushPayload{
				Ref:   "refs/heads/main",
				After: "af69d162de5a276abc86e0686b2b44033cd3f442",
				Repo: &gogsclient.Repository{
					HTMLURL: "https://gogs.example.com/example/repo",
				},
			},
		},
		"valid-gogs-push-event-with-secret": {
			secretData: map[string][]byte{
				gogsKey: []byte("gogssecret"),
			},
			body: []byte(`{
				"ref": "refs/heads/main",
				"after": "af69d162de5a276abc86e0686b2b44033cd3f442",
				"repository": {
					"html_url": "https://gogs.example.com/example/repo"
				}
			}`),
			headers: map[string]string{
				"X-Gogs-Event":     "push",
				"X-Gogs-Signature": "bd00de14f93a8bef25c89bb3c976a1320b3a535515cd66c91ec1e8b9f67a2259",
			},
			wantErr: false,
			wantEvent: gogsclient.PushPayload{
				Ref:   "refs/heads/main",
				After: "af69d162de5a276abc86e0686b2b44033cd3f442",
				Repo: &gogsclient.Repository{
					HTMLURL: "https://gogs.example.com/example/repo",
				},
			},
		},
		"invalid-gogs-push-event-with-secret": {
			secretData: map[string][]byte{
				gogsKey: []byte("gogssecret"),
			},
			body: []byte(`{
				"ref": "refs/heads/main",
				"after": "af69d162de5a276abc86e0686b2b44033cd3f442",
				"repository": {
					"html_url": "https://gogs.example.com/example/repo"
				}
			}`),
			headers: map[string]string{
				"X-Gogs-Event":     "push",
				"X-Gogs-Signature": "wrongsignature",
			},
			wantErr:    true,
			wantErrMsg: "HMAC verification failed",
			wantEvent:  nil,
		},
		"missing-gogs-secret": {
			secretData: map[string][]byte{
				"wrongkey": []byte("gogssecret"),
			},
			body: []byte(`{
				"ref": "refs/heads/main",
				"after": "af69d162de5a276abc86e0686b2b44033cd3f442",
				"repository": {
					"html_url": "https://gogs.example.com/example/repo"
				}
			}`),
			headers: map[string]string{
				"X-Gogs-Event":     "push",
				"X-Gogs-Signature": "3453557968570459352679905f2372454008f9015101a",
			},
			wantErr:    true,
			wantErrMsg: "secret key \"gogs\" not found in secret \"test-secret\"",
			wantEvent:  nil,
		},
		"no-gogs-event": {
			secretData: nil,
			body: []byte(`{
				"ref": "refs/heads/main",
				"after": "af69d162de5a276abc86e0686b2b44033cd3f442",
				"repository": {
					"html_url": "https://gogs.example.com/example/repo"
				}
			}`),
			headers:    map[string]string{},
			wantErr:    true,
			wantErrMsg: "missing X-Gogs-Event Header",
			wantEvent:  nil,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var secret *corev1.Secret
			if tt.secretData != nil {
				secret = &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret",
						Namespace: "test-ns",
					},
					Data: tt.secretData,
				}
			}

			req, err := http.NewRequest(http.MethodPost, "/", bytes.NewReader(tt.body))
			if err != nil {
				t.Fatalf("Failed to create HTTP request: %v", err)
			}

			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			got, err := parseGogs(req, secret)

			if tt.wantErr {
				assert.Error(t, err, tt.wantErrMsg)
				return
			}

			if err != nil {
				t.Fatalf("parseGogs() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantEvent == nil {
				if got != nil {
					t.Fatalf("parseGogs() = %v, want %v", got, tt.wantEvent)
				}
			} else {
				assert.DeepEqual(t, got, tt.wantEvent)
			}
		})
	}
}

func TestParseGithub(t *testing.T) {
	utilruntime.Must(corev1.AddToScheme(scheme.Scheme))

	tests := map[string]struct {
		secretData   map[string][]byte
		body         []byte
		headers      map[string]string
		wantErr      bool
		wantErrMsg   string
		wantNilEvent bool
		wantRef      string
		wantAfter    string
		wantRepoURL  string
	}{
		"valid-github-push-event-no-secret": {
			secretData: nil,
			body: []byte(`{
				"ref": "refs/heads/main",
				"after": "af69d162de5a276abc86e0686b2b44033cd3f442",
				"repository": {
					"html_url": "https://github.com/example/repo"
				}
			}`),
			headers: map[string]string{
				"X-GitHub-Event": "push",
			},
			wantErr:      false,
			wantNilEvent: false,
			wantRef:      "refs/heads/main",
			wantAfter:    "af69d162de5a276abc86e0686b2b44033cd3f442",
			wantRepoURL:  "https://github.com/example/repo",
		},
		"valid-github-push-event-with-secret": {
			secretData: map[string][]byte{
				githubKey: []byte("githubsecret"),
			},
			body: []byte(`{
				"ref": "refs/heads/main",
				"after": "af69d162de5a276abc86e0686b2b44033cd3f442",
				"repository": {
					"html_url": "https://github.com/example/repo"
				}
			}`),
			headers: map[string]string{
				"X-GitHub-Event":  "push",
				"X-Hub-Signature": "sha1=dba820f85951e0f100549aa167ef67dcd989ca4a",
			},
			wantErr:      false,
			wantNilEvent: false,
			wantRef:      "refs/heads/main",
			wantAfter:    "af69d162de5a276abc86e0686b2b44033cd3f442",
			wantRepoURL:  "https://github.com/example/repo",
		},
		"invalid-github-push-event-with-secret": {
			secretData: map[string][]byte{
				githubKey: []byte("githubsecret"),
			},
			body: []byte(`{
				"ref": "refs/heads/main",
				"after": "af69d162de5a276abc86e0686b2b44033cd3f442",
				"repository": {
					"html_url": "https://github.com/example/repo"
				}
			}`),
			headers: map[string]string{
				"X-GitHub-Event":  "push",
				"X-Hub-Signature": "sha1=wrongsignature",
			},
			wantErr:      true,
			wantErrMsg:   "HMAC verification failed",
			wantNilEvent: true,
		},
		"missing-github-secret": {
			secretData: map[string][]byte{
				"wrongkey": []byte("githubsecret"),
			},
			body: []byte(`{
				"ref": "refs/heads/main",
				"after": "af69d162de5a276abc86e0686b2b44033cd3f442",
				"repository": {
					"html_url": "https://github.com/example/repo"
				}
			}`),
			headers: map[string]string{
				"X-GitHub-Event":  "push",
				"X-Hub-Signature": "sha1=57968570459352679905f2372454008f9015101a",
			},
			wantErr:      true,
			wantNilEvent: true,
			wantErrMsg:   "secret key \"github\" not found in secret \"test-secret\"",
		},
		"no-github-event": {
			secretData: nil,
			body: []byte(`{
				"ref": "refs/heads/main",
				"after": "af69d162de5a276abc86e0686b2b44033cd3f442",
				"repository": {
					"html_url": "https://github.com/example/repo"
				}
			}`),
			headers:      map[string]string{},
			wantErr:      true,
			wantNilEvent: true,
			wantErrMsg:   "missing X-GitHub-Event Header",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var secret *corev1.Secret
			if tt.secretData != nil {
				secret = &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret",
						Namespace: "test-ns",
					},
					Data: tt.secretData,
				}
			}

			req, err := http.NewRequest(http.MethodPost, "/", bytes.NewReader(tt.body))
			if err != nil {
				t.Fatalf("Failed to create HTTP request: %v", err)
			}

			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			got, err := parseGithub(req, secret)

			if tt.wantErr {
				assert.Error(t, err, tt.wantErrMsg)
				return
			}

			if err != nil {
				t.Fatalf("parseGithub() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantNilEvent {
				if got != nil {
					t.Fatalf("parseGithub() got %v, want nil", got)
				}
			} else {
				if got == nil {
					t.Fatalf("parseGithub() got nil, want not nil")
				}
				gotGH, ok := got.(github.PushPayload)
				if !ok {
					t.Fatalf("parseGithub() got %T, want github.PushPayload", got)
				}

				if gotGH.Ref != tt.wantRef {
					t.Fatalf("parseGithub() got ref %s, want %s", gotGH.Ref, tt.wantRef)
				}
				if gotGH.After != tt.wantAfter {
					t.Fatalf("parseGithub() got after %s, want %s", gotGH.After, tt.wantAfter)
				}
				if gotGH.Repository.HTMLURL != tt.wantRepoURL {
					t.Fatalf("parseGithub() got repo URL %s, want %s", gotGH.Repository.HTMLURL, tt.wantRepoURL)
				}
			}
		})
	}
}

func TestParseGitlab(t *testing.T) {
	utilruntime.Must(corev1.AddToScheme(scheme.Scheme))

	tests := map[string]struct {
		secretData map[string][]byte
		body       []byte
		headers    map[string]string
		wantErr    bool
		wantErrMsg string
		wantEvent  interface{}
	}{
		"valid-gitlab-push-event-no-secret": {
			secretData: nil,
			body: []byte(`{
				"ref": "refs/heads/main",
				"checkout_sha": "af69d162de5a276abc86e0686b2b44033cd3f442",
				"project": {
					"web_url": "https://gitlab.example.com/example/repo"
				}
			}`),
			headers: map[string]string{
				"X-Gitlab-Event": "Push Hook",
			},
			wantErr: false,
			wantEvent: gitlab.PushEventPayload{
				Ref:         "refs/heads/main",
				CheckoutSHA: "af69d162de5a276abc86e0686b2b44033cd3f442",
				Project: gitlab.Project{
					WebURL: "https://gitlab.example.com/example/repo",
				},
			},
		},
		"valid-gitlab-push-event-with-secret": {
			secretData: map[string][]byte{
				gitlabKey: []byte("gitlabsecret"),
			},
			body: []byte(`{
				"ref": "refs/heads/main",
				"checkout_sha": "af69d162de5a276abc86e0686b2b44033cd3f442",
				"project": {
					"web_url": "https://gitlab.example.com/example/repo"
				}
			}`),
			headers: map[string]string{
				"X-Gitlab-Event": "Push Hook",
				"X-Gitlab-Token": "gitlabsecret",
			},
			wantErr: false,
			wantEvent: gitlab.PushEventPayload{
				Ref:         "refs/heads/main",
				CheckoutSHA: "af69d162de5a276abc86e0686b2b44033cd3f442",
				Project: gitlab.Project{
					WebURL: "https://gitlab.example.com/example/repo",
				},
			},
		},
		"invalid-gitlab-push-event-with-secret": {
			secretData: map[string][]byte{
				gitlabKey: []byte("gitlabsecret"),
			},
			body: []byte(`{
				"ref": "refs/heads/main",
				"checkout_sha": "af69d162de5a276abc86e0686b2b44033cd3f442",
				"project": {
					"web_url": "https://gitlab.example.com/example/repo"
				}
			}`),
			headers: map[string]string{
				"X-Gitlab-Event": "Push Hook",
				"X-Gitlab-Token": "wrongsecret",
			},
			wantErr:    true,
			wantErrMsg: "X-Gitlab-Token validation failed",
			wantEvent:  nil,
		},
		"missing-gitlab-secret": {
			secretData: map[string][]byte{
				"wrongkey": []byte("gitlabsecret"),
			},
			body: []byte(`{
				"ref": "refs/heads/main",
				"checkout_sha": "af69d162de5a276abc86e0686b2b44033cd3f442",
				"project": {
					"web_url": "https://gitlab.example.com/example/repo"
				}
			}`),
			headers: map[string]string{
				"X-Gitlab-Event": "Push Hook",
				"X-Gitlab-Token": "gitlabsecret",
			},
			wantErr:    true,
			wantErrMsg: "secret key \"gitlab\" not found in secret \"test-secret\"",
			wantEvent:  nil,
		},
		"no-gitlab-event": {
			secretData: nil,
			body: []byte(`{
				"ref": "refs/heads/main",
				"checkout_sha": "af69d162de5a276abc86e0686b2b44033cd3f442",
				"project": {
					"web_url": "https://gitlab.example.com/example/repo"
				}
			}`),
			headers:    map[string]string{},
			wantErr:    true,
			wantErrMsg: "missing X-Gitlab-Event Header",
			wantEvent:  nil,
		},
		"valid-gitlab-tag-event-no-secret": {
			secretData: nil,
			body: []byte(`{
				"ref": "refs/tags/v1.0.0",
				"checkout_sha": "af69d162de5a276abc86e0686b2b44033cd3f442",
				"project": {
					"web_url": "https://gitlab.example.com/example/repo"
				}
			}`),
			headers: map[string]string{
				"X-Gitlab-Event": "Tag Push Hook",
			},
			wantErr: false,
			wantEvent: gitlab.TagEventPayload{
				Ref:         "refs/tags/v1.0.0",
				CheckoutSHA: "af69d162de5a276abc86e0686b2b44033cd3f442",
				Project: gitlab.Project{
					WebURL: "https://gitlab.example.com/example/repo",
				},
			},
		},
		"valid-gitlab-tag-event-with-secret": {
			secretData: map[string][]byte{
				gitlabKey: []byte("gitlabsecret"),
			},
			body: []byte(`{
				"ref": "refs/tags/v1.0.0",
				"checkout_sha": "af69d162de5a276abc86e0686b2b44033cd3f442",
				"project": {
					"web_url": "https://gitlab.example.com/example/repo"
				}
			}`),
			headers: map[string]string{
				"X-Gitlab-Event": "Tag Push Hook",
				"X-Gitlab-Token": "gitlabsecret",
			},
			wantErr: false,
			wantEvent: gitlab.TagEventPayload{
				Ref:         "refs/tags/v1.0.0",
				CheckoutSHA: "af69d162de5a276abc86e0686b2b44033cd3f442",
				Project: gitlab.Project{
					WebURL: "https://gitlab.example.com/example/repo",
				},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var secret *corev1.Secret
			if tt.secretData != nil {
				secret = &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret",
						Namespace: "test-ns",
					},
					Data: tt.secretData,
				}
			}

			req, err := http.NewRequest(http.MethodPost, "/", bytes.NewReader(tt.body))
			if err != nil {
				t.Fatalf("Failed to create HTTP request: %v", err)
			}

			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			got, err := parseGitlab(req, secret)

			if tt.wantErr {
				assert.Error(t, err, tt.wantErrMsg)
				return
			}

			if err != nil {
				t.Fatalf("parseGitlab() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantEvent == nil {
				if got != nil {
					t.Fatalf("parseGitlab() = %v, want %v", got, tt.wantEvent)
				}
			} else {
				assert.DeepEqual(t, got, tt.wantEvent)
			}
		})
	}
}

func TestParseBitbucket(t *testing.T) {
	utilruntime.Must(corev1.AddToScheme(scheme.Scheme))

	tests := map[string]struct {
		secretData   map[string][]byte
		body         []byte
		headers      map[string]string
		wantErr      bool
		wantErrMsg   string
		wantNilEvent bool
		wantRepoURL  string
		wantBranch   string
		wantRevision string
	}{
		"valid-bitbucket-push-event-no-secret": {
			secretData: nil,
			body: []byte(`{
				"push": {
					"changes": [
						{
							"new": {
								"type": "branch",
								"name": "main",
								"target": {
									"hash": "af69d162de5a276abc86e0686b2b44033cd3f442"
								}
							}
						}
					]
				},
				"repository": {
					"links": {
						"html": {
							"href": "https://bitbucket.org/example/repo"
						}
					}
				}
			}`),
			headers: map[string]string{
				"X-Hook-UUID": "some-uuid",
				"X-Event-Key": "repo:push",
			},
			wantErr:      false,
			wantNilEvent: false,
			wantRepoURL:  "https://bitbucket.org/example/repo",
			wantBranch:   "main",
			wantRevision: "af69d162de5a276abc86e0686b2b44033cd3f442",
		},
		"valid-bitbucket-push-event-with-secret": {
			secretData: map[string][]byte{
				bitbucketKey: []byte("some-uuid"),
			},
			body: []byte(`{
				"push": {
					"changes": [
						{
							"new": {
								"type": "branch",
								"name": "main",
								"target": {
									"hash": "af69d162de5a276abc86e0686b2b44033cd3f442"
								}
							}
						}
					]
				},
				"repository": {
					"links": {
						"html": {
							"href": "https://bitbucket.org/example/repo"
						}
					}
				}
			}`),
			headers: map[string]string{
				"X-Hook-UUID": "some-uuid",
				"X-Event-Key": "repo:push",
			},
			wantErr:      false,
			wantNilEvent: false,
			wantRepoURL:  "https://bitbucket.org/example/repo",
			wantBranch:   "main",
			wantRevision: "af69d162de5a276abc86e0686b2b44033cd3f442",
		},
		"invalid-bitbucket-push-event-with-secret": {
			secretData: map[string][]byte{
				bitbucketKey: []byte("some-uuid"),
			},
			body: []byte(`{
				"push": {
					"changes": [
						{
							"new": {
								"type": "branch",
								"name": "main",
								"target": {
									"hash": "af69d162de5a276abc86e0686b2b44033cd3f442"
								}
							}
						}
					]
				},
				"repository": {
					"links": {
						"html": {
							"href": "https://bitbucket.org/example/repo"
						}
					}
				}
			}`),
			headers: map[string]string{
				"X-Hook-UUID": "wrong-uuid",
				"X-Event-Key": "repo:push",
			},
			wantErr:      true,
			wantErrMsg:   "UUID verification failed",
			wantNilEvent: true,
		},
		"missing-bitbucket-secret": {
			secretData: map[string][]byte{
				"wrongkey": []byte("some-uuid"),
			},
			body: []byte(`{
				"push": {
					"changes": [
						{
							"new": {
								"type": "branch",
								"name": "main",
								"target": {
									"hash": "af69d162de5a276abc86e0686b2b44033cd3f442"
								}
							}
						}
					]
				},
				"repository": {
					"links": {
						"html": {
							"href": "https://bitbucket.org/example/repo"
						}
					}
				}
			}`),
			headers: map[string]string{
				"X-Hook-UUID": "some-uuid",
				"X-Event-Key": "repo:push",
			},
			wantErr:      true,
			wantNilEvent: true,
			wantErrMsg:   "secret key \"bitbucket\" not found in secret \"test-secret\"",
		},
		"no-bitbucket-event": {
			secretData:   nil,
			body:         []byte(`{}`),
			headers:      map[string]string{},
			wantErr:      true,
			wantErrMsg:   "missing X-Event-Key Header",
			wantNilEvent: true,
		},
		"empty-changes": {
			secretData: nil,
			body: []byte(`{
				"push": {
					"changes": []
				},
				"repository": {
					"links": {
						"html": {
							"href": "https://bitbucket.org/example/repo"
						}
					}
				}
			}`),
			headers: map[string]string{
				"X-Hook-UUID": "some-uuid",
				"X-Event-Key": "repo:push",
			},
			wantErr:      false,
			wantNilEvent: false,
			wantRepoURL:  "https://bitbucket.org/example/repo",
			wantBranch:   "",
			wantRevision: "",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var secret *corev1.Secret
			if tt.secretData != nil {
				secret = &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret",
						Namespace: "test-ns",
					},
					Data: tt.secretData,
				}
			}

			req, err := http.NewRequest(http.MethodPost, "/", bytes.NewReader(tt.body))
			if err != nil {
				t.Fatalf("Failed to create HTTP request: %v", err)
			}

			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			got, err := parseBitbucket(req, secret)

			if tt.wantErr {
				assert.Error(t, err, tt.wantErrMsg)
				return
			}

			if err != nil {
				t.Fatalf("parseBitbucket() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantNilEvent {
				if got != nil {
					t.Fatalf("parseBitbucket() got %v, want nil", got)
				}
			} else {
				if got == nil {
					t.Fatalf("parseBitbucket() got nil, want not nil")
				}
				gotBB, ok := got.(bitbucket.RepoPushPayload)
				if !ok {
					t.Fatalf("parseBitbucket() got %T, want bitbucket.RepoPushPayload", got)
				}

				if gotBB.Repository.Links.HTML.Href != tt.wantRepoURL {
					t.Fatalf("parseBitbucket() got repo URL %s, want %s", gotBB.Repository.Links.HTML.Href, tt.wantRepoURL)
				}
				if len(gotBB.Push.Changes) > 0 {
					if gotBB.Push.Changes[0].New.Name != tt.wantBranch {
						t.Fatalf("parseBitbucket() got branch %s, want %s", gotBB.Push.Changes[0].New.Name, tt.wantBranch)
					}
					if gotBB.Push.Changes[0].New.Target.Hash != tt.wantRevision {
						t.Fatalf("parseBitbucket() got revision %s, want %s", gotBB.Push.Changes[0].New.Target.Hash, tt.wantRevision)
					}
				}
			}
		})
	}
}

func TestParseBitbucketServer(t *testing.T) {
	utilruntime.Must(corev1.AddToScheme(scheme.Scheme))

	tests := map[string]struct {
		secretData   map[string][]byte
		body         []byte
		headers      map[string]string
		wantErr      bool
		wantErrMsg   string
		wantNilEvent bool
		wantRepoURL  string
		wantBranch   string
		wantRevision string
	}{
		"valid-bitbucket-server-push-event-no-secret": {
			secretData: nil,
			body: []byte(`{
				"eventKey": "repo:refs_changed",
				"changes": [
					{
						"ref": {
							"id": "refs/heads/main",
							"type": "BRANCH"
						},
						"toHash": "af69d162de5a276abc86e0686b2b44033cd3f442"
					}
				],
				"repository": {
					"links": {
						"clone": [
							{
								"href": "https://bitbucket.example.com/scm/example/repo.git",
								"name": "http"
							},
							{
								"href": "ssh://git@bitbucket.example.com:7999/example/repo.git",
								"name": "ssh"
							}
						]
					}
				}
			}`),
			headers: map[string]string{
				"X-Event-Key": "repo:refs_changed",
			},
			wantErr:      false,
			wantNilEvent: false,
			wantRepoURL:  "https://bitbucket.example.com/scm/example/repo.git",
			wantBranch:   "main",
			wantRevision: "af69d162de5a276abc86e0686b2b44033cd3f442",
		},
		"valid-bitbucket-server-push-event-with-secret": {
			secretData: map[string][]byte{
				bitbucketServerKey: []byte("secret"),
			},
			body: []byte(`{
				"eventKey": "repo:refs_changed",
				"changes": [
					{
						"ref": {
							"id": "refs/heads/main",
							"type": "BRANCH"
						},
						"toHash": "af69d162de5a276abc86e0686b2b44033cd3f442"
					}
				],
				"repository": {
					"links": {
						"clone": [
							{
								"href": "https://bitbucket.example.com/scm/example/repo.git",
								"name": "http"
							},
							{
								"href": "ssh://git@bitbucket.example.com:7999/example/repo.git",
								"name": "ssh"
							}
						]
					}
				}
			}`),
			headers: map[string]string{
				"X-Event-Key":     "repo:refs_changed",
				"X-Hub-Signature": "sha256=31233f258e5af3c0571515b460749da4babc891a1fc6088229d7c2904cad83d1",
			},
			wantErr:      false,
			wantNilEvent: false,
			wantRepoURL:  "https://bitbucket.example.com/scm/example/repo.git",
			wantBranch:   "main",
			wantRevision: "af69d162de5a276abc86e0686b2b44033cd3f442",
		},
		"invalid-bitbucket-server-push-event-with-secret": {
			secretData: map[string][]byte{
				bitbucketServerKey: []byte("secret"),
			},
			body: []byte(`{
				"eventKey": "repo:refs_changed",
				"changes": [
					{
						"ref": {
							"id": "refs/heads/main",
							"type": "BRANCH"
						},
						"toHash": "af69d162de5a276abc86e0686b2b44033cd3f442"
					}
				],
				"repository": {
					"links": {
						"clone": [
							{
								"href": "https://bitbucket.example.com/scm/example/repo.git",
								"name": "http"
							},
							{
								"href": "ssh://git@bitbucket.example.com:7999/example/repo.git",
								"name": "ssh"
							}
						]
					}
				}
			}`),
			headers: map[string]string{
				"X-Event-Key":     "repo:refs_changed",
				"X-Hub-Signature": "sha256=wrongsignature",
			},
			wantErr:      true,
			wantErrMsg:   "HMAC verification failed",
			wantNilEvent: true,
		},
		"missing-bitbucket-server-secret": {
			secretData: map[string][]byte{
				"wrongkey": []byte("secret"),
			},
			body: []byte(`{
				"eventKey": "repo:refs_changed",
				"changes": [
					{
						"ref": {
							"id": "refs/heads/main",
							"type": "BRANCH"
						},
						"toHash": "af69d162de5a276abc86e0686b2b44033cd3f442"
					}
				],
				"repository": {
					"links": {
						"clone": [
							{
								"href": "https://bitbucket.example.com/scm/example/repo.git",
								"name": "http"
							},
							{
								"href": "ssh://git@bitbucket.example.com:7999/example/repo.git",
								"name": "ssh"
							}
						]
					}
				}
			}`),
			headers: map[string]string{
				"X-Event-Key":     "repo:refs_changed",
				"X-Hub-Signature": "sha256=6043b765891b367c98604263173510004462847074747103258054242901590a",
			},
			wantErr:      true,
			wantNilEvent: true,
			wantErrMsg:   "secret key \"bitbucket-server\" not found in secret \"test-secret\"",
		},
		"no-bitbucket-server-event": {
			secretData:   nil,
			body:         []byte(`{}`),
			headers:      map[string]string{},
			wantErr:      true,
			wantErrMsg:   "missing X-Event-Key Header",
			wantNilEvent: true,
		},
		"empty-changes": {
			secretData: nil,
			body: []byte(`{
				"eventKey": "repo:refs_changed",
				"changes": [],
				"repository": {
					"links": {
						"clone": [
							{
								"href": "https://bitbucket.example.com/scm/example/repo.git",
								"name": "http"
							},
							{
								"href": "ssh://git@bitbucket.example.com:7999/example/repo.git",
								"name": "ssh"
							}
						]
					}
				}
			}`),
			headers: map[string]string{
				"X-Event-Key": "repo:refs_changed",
			},
			wantErr:      false,
			wantNilEvent: false,
			wantRepoURL:  "https://bitbucket.example.com/scm/example/repo.git",
			wantBranch:   "",
			wantRevision: "",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var secret *corev1.Secret
			if tt.secretData != nil {
				secret = &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret",
						Namespace: "test-ns",
					},
					Data: tt.secretData,
				}
			}

			req, err := http.NewRequest(http.MethodPost, "/", bytes.NewReader(tt.body))
			if err != nil {
				t.Fatalf("Failed to create HTTP request: %v", err)
			}

			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			got, err := parseBitbucketServer(req, secret)

			if tt.wantErr {
				assert.Error(t, err, tt.wantErrMsg)
				return
			}

			if err != nil {
				t.Fatalf("parseBitbucketServer() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantNilEvent {
				if got != nil {
					t.Fatalf("parseBitbucketServer() got %v, want nil", got)
				}
			} else {
				if got == nil {
					t.Fatalf("parseBitbucketServer() got nil, want not nil")
				}
				gotBB, ok := got.(bitbucketserver.RepositoryReferenceChangedPayload)
				if !ok {
					t.Fatalf("parseBitbucketServer() got %T, want bitbucketserver.RepositoryReferenceChangedPayload", got)
				}

				_, okCloneLinks := gotBB.Repository.Links["clone"]
				if okCloneLinks {
					for _, l := range gotBB.Repository.Links["clone"].([]interface{}) {
						link := l.(map[string]interface{})
						if link["name"] == "http" {
							if link["href"].(string) != tt.wantRepoURL {
								t.Fatalf("parseBitbucketServer() got repo URL %s, want %s", link["href"].(string), tt.wantRepoURL)
							}
						}
					}
				}

				if len(gotBB.Changes) > 0 {
					if gotBB.Changes[0].Reference.ID != "refs/heads/"+tt.wantBranch && gotBB.Changes[0].Reference.ID != "refs/tags/"+tt.wantBranch {
						t.Fatalf("parseBitbucketServer() got branch %s, want %s", gotBB.Changes[0].ReferenceID, tt.wantBranch)
					}
					if gotBB.Changes[0].ToHash != tt.wantRevision {
						t.Fatalf("parseBitbucketServer() got revision %s, want %s", gotBB.Changes[0].ToHash, tt.wantRevision)
					}
				}
			}
		})
	}
}

func TestParseAzureDevops(t *testing.T) {
	utilruntime.Must(corev1.AddToScheme(scheme.Scheme))

	tests := map[string]struct {
		secretData   map[string][]byte
		body         []byte
		headers      map[string]string
		wantErr      bool
		wantErrMsg   string
		wantNilEvent bool
		wantRepoURL  string
		wantBranch   string
		wantRevision string
	}{
		"valid-azure-devops-push-event-no-secret": {
			secretData: nil,
			body: []byte(`{
				"eventType": "git.push",
				"resource": {
					"refUpdates": [
						{
							"name": "refs/heads/main",
							"newObjectId": "af69d162de5a276abc86e0686b2b44033cd3f442"
						}
					],
					"repository": {
						"remoteUrl": "https://dev.azure.com/example/repo/_git/repo"
					}
				}
			}`),
			headers: map[string]string{
				"X-Vss-Activityid": "some-activity-id",
			},
			wantErr:      false,
			wantNilEvent: false,
			wantRepoURL:  "https://dev.azure.com/example/repo/_git/repo",
			wantBranch:   "main",
			wantRevision: "af69d162de5a276abc86e0686b2b44033cd3f442",
		},
		"valid-azure-devops-push-event-with-secret": {
			secretData: map[string][]byte{
				azureUsername: []byte("Aladdin"),
				azurePassword: []byte("open sesame"),
			},
			body: []byte(`{
				"eventType": "git.push",
				"resource": {
					"refUpdates": [
						{
							"name": "refs/heads/main",
							"newObjectId": "af69d162de5a276abc86e0686b2b44033cd3f442"
						}
					],
					"repository": {
						"remoteUrl": "https://dev.azure.com/example/repo/_git/repo"
					}
				}
			}`),
			headers: map[string]string{
				"X-Vss-Activityid": "some-activity-id",
				"Authorization":    "Basic QWxhZGRpbjpvcGVuIHNlc2FtZQ==",
			},
			wantErr:      false,
			wantNilEvent: false,
			wantRepoURL:  "https://dev.azure.com/example/repo/_git/repo",
			wantBranch:   "main",
			wantRevision: "af69d162de5a276abc86e0686b2b44033cd3f442",
		},
		"missing-azure-devops-username-secret": {
			secretData: map[string][]byte{
				"wrongkey":    []byte("testuser"),
				azurePassword: []byte("testpassword"),
			},
			body: []byte(`{
				"eventType": "git.push",
				"resource": {
					"refUpdates": [
						{
							"name": "refs/heads/main",
							"newObjectId": "af69d162de5a276abc86e0686b2b44033cd3f442"
						}
					],
					"repository": {
						"remoteUrl": "https://dev.azure.com/example/repo/_git/repo"
					}
				}
			}`),
			headers: map[string]string{
				"X-Vss-Activityid": "some-activity-id",
			},
			wantErr:      true,
			wantNilEvent: true,
			wantErrMsg:   "secret key \"azure-username\" not found in secret \"test-secret\"",
		},
		"missing-azure-devops-password-secret": {
			secretData: map[string][]byte{
				azureUsername: []byte("testuser"),
				"wrongkey":    []byte("testpassword"),
			},
			body: []byte(`{
				"eventType": "git.push",
				"resource": {
					"refUpdates": [
						{
							"name": "refs/heads/main",
							"newObjectId": "af69d162de5a276abc86e0686b2b44033cd3f442"
						}
					],
					"repository": {
						"remoteUrl": "https://dev.azure.com/example/repo/_git/repo"
					}
				}
			}`),
			headers: map[string]string{
				"X-Vss-Activityid": "some-activity-id",
			},
			wantErr:      true,
			wantNilEvent: true,
			wantErrMsg:   "secret key \"azure-password\" not found in secret \"test-secret\"",
		},
		"no-azure-devops-event": {
			secretData:   nil,
			body:         []byte(`{}`),
			headers:      map[string]string{},
			wantErr:      true,
			wantErrMsg:   "unknown event ",
			wantNilEvent: true,
		},
		"empty-refUpdates": {
			secretData: nil,
			body: []byte(`{
				"eventType": "git.push",
				"resource": {
					"refUpdates": [],
					"repository": {
						"remoteUrl": "https://dev.azure.com/example/repo/_git/repo"
					}
				}
			}`),
			headers: map[string]string{
				"X-Vss-Activityid": "some-activity-id",
			},
			wantErr:      false,
			wantNilEvent: false,
			wantRepoURL:  "https://dev.azure.com/example/repo/_git/repo",
			wantBranch:   "",
			wantRevision: "",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var secret *corev1.Secret
			if tt.secretData != nil {
				secret = &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret",
						Namespace: "test-ns",
					},
					Data: tt.secretData,
				}
			}

			req, err := http.NewRequest(http.MethodPost, "/", bytes.NewReader(tt.body))
			if err != nil {
				t.Fatalf("Failed to create HTTP request: %v", err)
			}

			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			got, err := parseAzureDevops(req, secret)

			if tt.wantErr {
				assert.Error(t, err, tt.wantErrMsg)
				return
			}

			if err != nil {
				t.Fatalf("parseAzureDevops() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantNilEvent {
				if got != nil {
					t.Fatalf("parseAzureDevops() got %v, want nil", got)
				}
			} else {
				if got == nil {
					t.Fatalf("parseAzureDevops() got nil, want not nil")
				}
				gotAzure, ok := got.(azuredevops.GitPushEvent)
				if !ok {
					t.Fatalf("parseAzureDevops() got %T, want azuredevops.GitPushEvent", got)
				}

				if gotAzure.Resource.Repository.RemoteURL != tt.wantRepoURL {
					t.Fatalf("parseAzureDevops() got repo URL %s, want %s", gotAzure.Resource.Repository.RemoteURL, tt.wantRepoURL)
				}
				if len(gotAzure.Resource.RefUpdates) > 0 {
					if gotAzure.Resource.RefUpdates[0].Name != "refs/heads/"+tt.wantBranch && gotAzure.Resource.RefUpdates[0].Name != "refs/tags/"+tt.wantBranch {
						t.Fatalf("parseAzureDevops() got branch %s, want %s", gotAzure.Resource.RefUpdates[0].Name, tt.wantBranch)
					}
					if gotAzure.Resource.RefUpdates[0].NewObjectID != tt.wantRevision {
						t.Fatalf("parseAzureDevops() got revision %s, want %s", gotAzure.Resource.RefUpdates[0].NewObjectID, tt.wantRevision)
					}
				}
			}
		})
	}
}
