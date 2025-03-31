package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"time"

	"go.uber.org/mock/gomock"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubectl/pkg/scheme"

	"github.com/go-playground/webhooks/v6/azuredevops"
	"github.com/rancher/fleet/internal/mocks"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"gopkg.in/go-playground/webhooks.v5/bitbucket"
	bitbucketserver "gopkg.in/go-playground/webhooks.v5/bitbucket-server"
	"gopkg.in/go-playground/webhooks.v5/github"
	"gopkg.in/go-playground/webhooks.v5/gitlab"
	"gopkg.in/go-playground/webhooks.v5/gogs"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	cfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"net/http"
	"net/http/httptest"
	"testing"

	"gotest.tools/assert"
)

type errReader int

func (errReader) Read(p []byte) (n int, err error) {
	return 0, fmt.Errorf("ERROR READING")
}

func TestGetBranchTagFromRef(t *testing.T) {
	inputs := []string{
		"refs/heads/master",
		"refs/heads/test",
		"refs/head/foo",
		"refs/tags/v0.1.1",
		"refs/tags/v0.1.2",
		"refs/tag/v0.1.3",
	}

	outputs := [][]string{
		{"master", ""},
		{"test", ""},
		{"", ""},
		{"", "v0.1.1"},
		{"", "v0.1.2"},
		{"", ""},
	}

	for i, input := range inputs {
		branch, tag := getBranchTagFromRef(input)
		assert.Equal(t, branch, outputs[i][0])
		assert.Equal(t, tag, outputs[i][1])
	}
}

func TestAzureDevopsWebhook(t *testing.T) {
	const commit = "f00c3a181697bb3829a6462e931c7456bbed557b"
	const repoURL = "https://dev.azure.com/fleet/git-test/_git/git-test"
	gitRepo := &v1alpha1.GitRepo{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Spec: v1alpha1.GitRepoSpec{
			Repo:   repoURL,
			Branch: "main",
		},
	}
	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	client := cfake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(gitRepo).WithStatusSubresource(gitRepo).Build()
	w := &Webhook{client: client}
	jsonBody := []byte(`{"subscriptionId":"xxx","notificationId":1,"id":"xxx","eventType":"git.push","publisherId":"tfs","message":{"text":"commit pushed","html":"commit pushed"},"detailedMessage":{"text":"pushed a commit to git-test"},"resource":{"commits":[{"commitId":"` + commit + `","author":{"name":"fleet","email":"fleet@suse.com","date":"2024-01-05T10:16:56Z"},"committer":{"name":"fleet","email":"fleet@suse.com","date":"2024-01-05T10:16:56Z"},"comment":"test commit","url":"https://dev.azure.com/fleet/_apis/git/repositories/xxx/commits/f00c3a181697bb3829a6462e931c7456bbed557b"}],"refUpdates":[{"name":"refs/heads/main","oldObjectId":"135f8a827edae980466f72eef385881bb4e158d8","newObjectId":"` + commit + `"}],"repository":{"id":"xxx","name":"git-test","url":"https://dev.azure.com/fleet/_apis/git/repositories/xxx","project":{"id":"xxx","name":"git-test","url":"https://dev.azure.com/fleet/_apis/projects/xxx","state":"wellFormed","visibility":"unchanged","lastUpdateTime":"0001-01-01T00:00:00"},"defaultBranch":"refs/heads/main","remoteUrl":"` + repoURL + `"},"pushedBy":{"displayName":"Fleet","url":"https://spsprodneu1.vssps.visualstudio.com/xxx/_apis/Identities/xxx","_links":{"avatar":{"href":"https://dev.azure.com/fleet/_apis/GraphProfile/MemberAvatars/msa.xxxx"}},"id":"xxx","uniqueName":"fleet@suse.com","imageUrl":"https://dev.azure.com/fleet/_api/_common/identityImage?id=xxx","descriptor":"xxxx"},"pushId":22,"date":"2024-01-05T10:17:18.735088Z","url":"https://dev.azure.com/fleet/_apis/git/repositories/xxx/pushes/22","_links":{"self":{"href":"https://dev.azure.com/fleet/_apis/git/repositories/xxx/pushes/22"},"repository":{"href":"https://dev.azure.com/fleet/xxx/_apis/git/repositories/xxx"},"commits":{"href":"https://dev.azure.com/fleet/_apis/git/repositories/xxx/pushes/22/commits"},"pusher":{"href":"https://spsprodneu1.vssps.visualstudio.com/xxx/_apis/Identities/xxx"},"refs":{"href":"https://dev.azure.com/fleet/xxx/_apis/git/repositories/xxx/refs/heads/main"}}},"resourceVersion":"1.0","resourceContainers":{"collection":{"id":"xxx","baseUrl":"https://dev.azure.com/fleet/"},"account":{"id":"ec365173-fce3-4dfc-8fc2-950f0b5728b1","baseUrl":"https://dev.azure.com/fleet/"},"project":{"id":"xxx","baseUrl":"https://dev.azure.com/fleet/"}},"createdDate":"2024-01-05T10:17:26.0098694Z"}`)
	bodyReader := bytes.NewReader(jsonBody)
	req, err := http.NewRequest(http.MethodPost, repoURL, bodyReader)
	if err != nil {
		t.Errorf("unexpected err %v", err)
	}
	h := http.Header{}
	h.Add("X-Vss-Activityid", "xxx")
	req.Header = h

	w.ServeHTTP(&responseWriter{}, req)

	updatedGitRepo := &v1alpha1.GitRepo{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: gitRepo.Name, Namespace: gitRepo.Namespace}, updatedGitRepo)
	if err != nil {
		t.Errorf("unexpected err %v", err)
	}
	if updatedGitRepo.Status.WebhookCommit != commit {
		t.Errorf("expected webhook commit %v, but got %v", commit, updatedGitRepo.Status.WebhookCommit)
	}
}

func TestAzureDevopsWebhookWithSSHURL(t *testing.T) {
	const (
		commit            = "f00c3a181697bb3829a6462e931c7456bbed557b"
		gitRepoURL        = "git@ssh.dev.azure.com:v3/fleet/git-test/git-test"
		responseRemoteURL = "https://dev.azure.com/fleet/git-test/_git/git-test"
	)

	gitRepo := &v1alpha1.GitRepo{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Spec: v1alpha1.GitRepoSpec{
			Repo:   gitRepoURL,
			Branch: "main",
		},
	}
	scheme := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	client := cfake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(gitRepo).WithStatusSubresource(gitRepo).Build()
	w := &Webhook{client: client}
	jsonBody := []byte(`{"subscriptionId":"xxx","notificationId":1,"id":"xxx","eventType":"git.push","publisherId":"tfs","message":{"text":"commit pushed","html":"commit pushed"},"detailedMessage":{"text":"pushed a commit to git-test"},"resource":{"commits":[{"commitId":"` + commit + `","author":{"name":"fleet","email":"fleet@suse.com","date":"2024-01-05T10:16:56Z"},"committer":{"name":"fleet","email":"fleet@suse.com","date":"2024-01-05T10:16:56Z"},"comment":"test commit","url":"https://dev.azure.com/fleet/_apis/git/repositories/xxx/commits/f00c3a181697bb3829a6462e931c7456bbed557b"}],"refUpdates":[{"name":"refs/heads/main","oldObjectId":"135f8a827edae980466f72eef385881bb4e158d8","newObjectId":"` + commit + `"}],"repository":{"id":"xxx","name":"git-test","url":"https://dev.azure.com/fleet/_apis/git/repositories/xxx","project":{"id":"xxx","name":"git-test","url":"https://dev.azure.com/fleet/_apis/projects/xxx","state":"wellFormed","visibility":"unchanged","lastUpdateTime":"0001-01-01T00:00:00"},"defaultBranch":"refs/heads/main","remoteUrl":"` + responseRemoteURL + `"},"pushedBy":{"displayName":"Fleet","url":"https://spsprodneu1.vssps.visualstudio.com/xxx/_apis/Identities/xxx","_links":{"avatar":{"href":"https://dev.azure.com/fleet/_apis/GraphProfile/MemberAvatars/msa.xxxx"}},"id":"xxx","uniqueName":"fleet@suse.com","imageUrl":"https://dev.azure.com/fleet/_api/_common/identityImage?id=xxx","descriptor":"xxxx"},"pushId":22,"date":"2024-01-05T10:17:18.735088Z","url":"https://dev.azure.com/fleet/_apis/git/repositories/xxx/pushes/22","_links":{"self":{"href":"https://dev.azure.com/fleet/_apis/git/repositories/xxx/pushes/22"},"repository":{"href":"https://dev.azure.com/fleet/xxx/_apis/git/repositories/xxx"},"commits":{"href":"https://dev.azure.com/fleet/_apis/git/repositories/xxx/pushes/22/commits"},"pusher":{"href":"https://spsprodneu1.vssps.visualstudio.com/xxx/_apis/Identities/xxx"},"refs":{"href":"https://dev.azure.com/fleet/xxx/_apis/git/repositories/xxx/refs/heads/main"}}},"resourceVersion":"1.0","resourceContainers":{"collection":{"id":"xxx","baseUrl":"https://dev.azure.com/fleet/"},"account":{"id":"ec365173-fce3-4dfc-8fc2-950f0b5728b1","baseUrl":"https://dev.azure.com/fleet/"},"project":{"id":"xxx","baseUrl":"https://dev.azure.com/fleet/"}},"createdDate":"2024-01-05T10:17:26.0098694Z"}`)
	bodyReader := bytes.NewReader(jsonBody)
	req, err := http.NewRequest(http.MethodPost, responseRemoteURL, bodyReader)
	if err != nil {
		t.Errorf("unexpected err %v", err)
	}
	h := http.Header{}
	h.Add("X-Vss-Activityid", "xxx")
	req.Header = h

	w.ServeHTTP(&responseWriter{}, req)

	updatedGitRepo := &v1alpha1.GitRepo{}
	err = client.Get(context.TODO(), types.NamespacedName{Name: gitRepo.Name, Namespace: gitRepo.Namespace}, updatedGitRepo)
	if err != nil {
		t.Errorf("unexpected err %v", err)
	}
	if updatedGitRepo.Status.WebhookCommit != commit {
		t.Errorf("expected webhook commit %v, but got %v", commit, updatedGitRepo.Status.WebhookCommit)
	}
}

type responseWriter struct{}

func (r *responseWriter) Header() http.Header {
	return http.Header{}
}
func (r *responseWriter) Write([]byte) (int, error) {
	return 0, nil
}

func (r *responseWriter) WriteHeader(statusCode int) {}

func TestGitHubPingWebhook(t *testing.T) {
	const zenMessage = "Keep it logically awesome."
	const hookID = 123456

	// GitRepo creation
	gitRepo := &v1alpha1.GitRepo{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Spec: v1alpha1.GitRepoSpec{
			Repo:   "https://github.com/example/repo",
			Branch: "main",
		},
	}

	// Kubernetes scheme and client configuration
	sch := scheme.Scheme
	utilruntime.Must(corev1.AddToScheme(sch))
	utilruntime.Must(v1alpha1.AddToScheme(sch))

	client := cfake.NewClientBuilder().WithScheme(sch).WithRuntimeObjects(gitRepo).Build()

	// Webhook initialisation
	w := &Webhook{
		client:    client,
		namespace: "default",
	}

	// JSON payload for the ping event
	jsonBody := []byte(fmt.Sprintf(`{
		"zen": "%s",
		"hook_id": %d,
		"hook": {
			"type": "Repository",
			"id": %d,
			"name": "web",
			"active": true,
			"events": [
				"push",
				"pull_request"
			],
			"config": {
				"content_type": "json",
				"url": "https://github.com/example/repo"
			},
			"updated_at": "2020-01-01T00:00:00Z",
			"created_at": "2020-01-01T00:00:00Z",
			"url": "https://api.github.com/repos/example/repo/hooks/%d",
			"test_url": "https://api.github.com/repos/example/repo/hooks/%d/test",
			"ping_url": "https://api.github.com/repos/example/repo/hooks/%d/pings"
		}
	}`, zenMessage, hookID, hookID, hookID, hookID, hookID))

	// Request creation
	req, err := http.NewRequest(http.MethodPost, "/", bytes.NewReader(jsonBody))
	if err != nil {
		t.Fatalf("Failed to create HTTP request: %v", err)
	}
	req.Header.Set("X-Github-Event", "ping")

	// request execution
	rr := httptest.NewRecorder()
	w.ServeHTTP(rr, req)

	// Verify the response status code is correct
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// Verify the response message is correct
	expectedResponse := "Webhook received successfully"
	if rr.Body.String() != expectedResponse {
		t.Errorf("handler returned unexpected body: got %v want %v", rr.Body, expectedResponse)
	}
}

func TestAuthErrorCodes(t *testing.T) {
	tests := map[string]struct {
		err               error
		expectedErrorCode int
	}{
		"gogs-verification": {
			err:               gogs.ErrHMACVerificationFailed,
			expectedErrorCode: http.StatusUnauthorized,
		},
		"gogs-no-verification": {
			err:               gogs.ErrInvalidHTTPMethod,
			expectedErrorCode: http.StatusMethodNotAllowed,
		},
		"github-verification": {
			err:               github.ErrHMACVerificationFailed,
			expectedErrorCode: http.StatusUnauthorized,
		},
		"github-no-verification": {
			err:               github.ErrEventNotFound,
			expectedErrorCode: http.StatusInternalServerError,
		},
		"gitlab-verification": {
			err:               gitlab.ErrGitLabTokenVerificationFailed,
			expectedErrorCode: http.StatusUnauthorized,
		},
		"gitlab-no-verification": {
			err:               gitlab.ErrMissingGitLabEventHeader,
			expectedErrorCode: http.StatusInternalServerError,
		},
		"bitbucket-verification": {
			err:               bitbucket.ErrUUIDVerificationFailed,
			expectedErrorCode: http.StatusUnauthorized,
		},
		"bitbucket-no-verification": {
			err:               bitbucket.ErrEventNotFound,
			expectedErrorCode: http.StatusInternalServerError,
		},
		"bitbucketserver-verification": {
			err:               bitbucketserver.ErrHMACVerificationFailed,
			expectedErrorCode: http.StatusUnauthorized,
		},
		"bitbucketserver-no-verification": {
			err:               bitbucketserver.ErrEventNotFound,
			expectedErrorCode: http.StatusInternalServerError,
		},
		"azure-verification": {
			err:               azuredevops.ErrBasicAuthVerificationFailed,
			expectedErrorCode: http.StatusUnauthorized,
		},
		"azure-no-verification": {
			err:               azuredevops.ErrInvalidHTTPMethod,
			expectedErrorCode: http.StatusMethodNotAllowed,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			errCode := getErrorCodeFromErr(test.err)

			if errCode != test.expectedErrorCode {
				t.Errorf("expected error code does not match. Got %d, expected %d", errCode, test.expectedErrorCode)
			}
		})
	}
}

func TestGitHubWrongSecret(t *testing.T) {
	// GitRepo creation
	gitRepo := &v1alpha1.GitRepo{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Spec: v1alpha1.GitRepoSpec{
			Repo:   "https://github.com/example/repo",
			Branch: "main",
		},
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      webhookSecretName,
			Namespace: "default",
		},
		Data: map[string][]byte{
			"github": []byte("badsecret"),
		},
	}

	// Kubernetes scheme and client configuration
	sch := scheme.Scheme
	err := v1alpha1.AddToScheme(sch)
	if err != nil {
		t.Fatalf("unable to add to scheme: %v", err)
	}
	client := cfake.NewClientBuilder().WithScheme(sch).WithRuntimeObjects(gitRepo, secret).Build()

	// Webhook initialisation
	w := &Webhook{
		client:    client,
		namespace: "default",
	}

	jsonBody := []byte(`
	{
	  "ref":"refs/heads/main",
	  "after":"af69d162de5a276abc86e0686b2b44033cd3f442",
	  "repository":{
		"html_url":"https://github.com/example/repo"
      }
    }`)

	// Request creation
	req, err := http.NewRequest(http.MethodPost, "/", bytes.NewReader(jsonBody))
	if err != nil {
		t.Fatalf("Failed to create HTTP request: %v", err)
	}
	req.Header.Set("X-Github-Event", "push")
	// calculate the value to store in the X-Hub-Signature header
	mac := hmac.New(sha1.New, []byte("supersecretvalue"))
	_, _ = mac.Write(jsonBody)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))
	req.Header.Set("X-Hub-Signature", fmt.Sprintf("sha1=%s", expectedMAC))

	// request execution
	rr := httptest.NewRecorder()
	w.ServeHTTP(rr, req)

	// Verify the response status code is correct
	if status := rr.Code; status != http.StatusUnauthorized {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusUnauthorized)
	}
}

func TestGitHubSecretAndCommitUpdated(t *testing.T) {
	expectedCommit := "af69d162de5a276abc86e0686b2b44033cd3f442"
	gitrepoSecretName := "gitrepoSecret"
	tests := map[string]struct {
		secretValueInWebhook string
		globalSecret         bool
		gitrepoSecret        bool
		globalSecretKey      string
		globalSecretValue    string
		gitrepoSecretKey     string
		gitrepoSecretValue   string
		setSecretInGitrepo   bool
		expectedResCode      int
		expectedCommitUpdate bool
	}{
		"global-secret-ok-no-gitrepo-secret": {
			secretValueInWebhook: "supersecretvalue",
			globalSecret:         true,
			gitrepoSecret:        false,
			globalSecretKey:      "github",
			globalSecretValue:    "supersecretvalue",
			gitrepoSecretKey:     "",
			gitrepoSecretValue:   "",
			setSecretInGitrepo:   false,
			expectedResCode:      http.StatusOK,
			expectedCommitUpdate: true,
		},
		"global-secret-wrong-no-gitrepo-secret": {
			secretValueInWebhook: "supersecretvalue",
			globalSecret:         true,
			gitrepoSecret:        false,
			globalSecretKey:      "github",
			globalSecretValue:    "bad-secret",
			gitrepoSecretKey:     "",
			gitrepoSecretValue:   "",
			setSecretInGitrepo:   false,
			expectedResCode:      http.StatusUnauthorized,
			expectedCommitUpdate: false,
		},
		"global-secret-ok-gitrepo-secret-ok": {
			secretValueInWebhook: "supersecretvalue",
			globalSecret:         true,
			gitrepoSecret:        true,
			globalSecretKey:      "github",
			globalSecretValue:    "supersecretvalue",
			gitrepoSecretKey:     "github",
			gitrepoSecretValue:   "supersecretvalue",
			setSecretInGitrepo:   true,
			expectedResCode:      http.StatusOK,
			expectedCommitUpdate: true,
		},
		// does not matter that global secret is wrong because
		// gitrepo secret takes preference
		"global-secret-wrong-gitrepo-secret-ok": {
			secretValueInWebhook: "supersecretvalue",
			globalSecret:         true,
			gitrepoSecret:        true,
			globalSecretKey:      "github",
			globalSecretValue:    "bad-secret",
			gitrepoSecretKey:     "github",
			gitrepoSecretValue:   "supersecretvalue",
			setSecretInGitrepo:   true,
			expectedResCode:      http.StatusOK,
			expectedCommitUpdate: true,
		},
		// does not matter that global secret is correct because
		// gitrepo secret takes preference
		"global-secret-ok-gitrepo-secret-wrong": {
			secretValueInWebhook: "supersecretvalue",
			globalSecret:         true,
			gitrepoSecret:        true,
			globalSecretKey:      "github",
			globalSecretValue:    "supersecretvalue",
			gitrepoSecretKey:     "github",
			gitrepoSecretValue:   "bad-secret",
			setSecretInGitrepo:   true,
			expectedResCode:      http.StatusUnauthorized,
			expectedCommitUpdate: false,
		},
		"no-global-secret-gitrepo-secret-wrong": {
			secretValueInWebhook: "supersecretvalue",
			globalSecret:         false,
			gitrepoSecret:        true,
			globalSecretKey:      "",
			globalSecretValue:    "",
			gitrepoSecretKey:     "github",
			gitrepoSecretValue:   "bad-secret",
			setSecretInGitrepo:   true,
			expectedResCode:      http.StatusUnauthorized,
			expectedCommitUpdate: false,
		},
		"no-global-secret-gitrepo-secret-ok": {
			secretValueInWebhook: "supersecretvalue",
			globalSecret:         false,
			gitrepoSecret:        true,
			globalSecretKey:      "",
			globalSecretValue:    "",
			gitrepoSecretKey:     "github",
			gitrepoSecretValue:   "supersecretvalue",
			setSecretInGitrepo:   true,
			expectedResCode:      http.StatusOK,
			expectedCommitUpdate: true,
		},
		"global-secret-wrong-key-no-gitrepo-secret": {
			secretValueInWebhook: "supersecretvalue",
			globalSecret:         true,
			gitrepoSecret:        false,
			globalSecretKey:      "github-bad-key",
			globalSecretValue:    "supersecretvalue",
			gitrepoSecretKey:     "",
			gitrepoSecretValue:   "",
			setSecretInGitrepo:   false,
			expectedResCode:      http.StatusInternalServerError,
			expectedCommitUpdate: false,
		},
		// does not matter that global secret is ok
		// because gitrepo secret takes preference
		"global-secret-ok-gitrepo-secret-wrong-key": {
			secretValueInWebhook: "supersecretvalue",
			globalSecret:         true,
			gitrepoSecret:        true,
			globalSecretKey:      "github",
			globalSecretValue:    "supersecretvalue",
			gitrepoSecretKey:     "github-bad-key",
			gitrepoSecretValue:   "supersecretvalue",
			setSecretInGitrepo:   true,
			expectedResCode:      http.StatusInternalServerError,
			expectedCommitUpdate: false,
		},
		// when no secret is defined we accept the payload
		"no-global-secret-no-gitrepo-secret": {
			secretValueInWebhook: "supersecretvalue",
			globalSecret:         false,
			gitrepoSecret:        false,
			globalSecretKey:      "",
			globalSecretValue:    "",
			gitrepoSecretKey:     "",
			gitrepoSecretValue:   "",
			setSecretInGitrepo:   false,
			expectedResCode:      http.StatusOK,
			expectedCommitUpdate: true,
		},

		// when no secret is defined we accept the payload
		"no-global-secret-no-gitrepo-secret-secret-in-gitrepo": {
			secretValueInWebhook: "supersecretvalue",
			globalSecret:         false,
			gitrepoSecret:        false,
			globalSecretKey:      "",
			globalSecretValue:    "",
			gitrepoSecretKey:     "",
			gitrepoSecretValue:   "",
			setSecretInGitrepo:   true,
			expectedResCode:      http.StatusInternalServerError,
			expectedCommitUpdate: false,
		},
	}
	for _, tt := range tests {
		ctlr := gomock.NewController(t)
		mockClient := mocks.NewMockClient(ctlr)

		gitRepo := &v1alpha1.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "gitrepoNamespace",
			},
			Spec: v1alpha1.GitRepoSpec{
				Repo:   "https://github.com/example/repo",
				Branch: "main",
			},
		}

		if tt.setSecretInGitrepo {
			gitRepo.Spec.WebhookSecret = gitrepoSecretName
		}

		// List GitRepos mock call
		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
			func(ctx context.Context, list *v1alpha1.GitRepoList, opts ...client.ListOption) error {
				list.Items = append(list.Items, *gitRepo)
				return nil
			},
		)

		// call for secret
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, name types.NamespacedName, secret *corev1.Secret, _ ...interface{}) error {
				// check that we're calling Get with the expected name and Namespace
				if tt.gitrepoSecret {
					if name.Name != gitrepoSecretName {
						t.Errorf("expecting calling secret Get with secret name %q, got %q", gitrepoSecretName, name.Name)
					}
					if name.Namespace != gitRepo.Namespace {
						t.Errorf("expecting calling secret Get with secret namespace %q, got %q", gitRepo.Namespace, name.Namespace)
					}
				} else if tt.globalSecret {
					// it expects the global secret name
					if name.Name != webhookSecretName {
						t.Errorf("expecting calling secret Get with secret name %q, got %q", webhookSecretName, name.Name)
					}
					// we're using "default" as the namespace for the webhook
					if name.Namespace != "default" {
						t.Errorf("expecting calling secret Get with secret namespace %q, got %q", "default", name.Namespace)
					}
				}
				if tt.gitrepoSecret {
					secret.Data = map[string][]byte{
						tt.gitrepoSecretKey: []byte(tt.gitrepoSecretValue),
					}
					return nil
				} else if tt.globalSecret {
					secret.Data = map[string][]byte{
						tt.globalSecretKey: []byte(tt.globalSecretValue),
					}
					return nil
				}

				// if no secret
				return errors.NewNotFound(schema.GroupResource{}, "")
			}).Times(1)

		// Status().Update() mock call
		if tt.expectedCommitUpdate {
			mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, name types.NamespacedName, gitrepo *v1alpha1.GitRepo, _ ...interface{}) error {
					return nil
				})
			statusClient := mocks.NewMockSubResourceWriter(ctlr)

			mockClient.EXPECT().Status().Return(statusClient).Times(1)
			statusClient.EXPECT().Patch(gomock.Any(), gomock.Any(), gomock.Any()).Do(
				func(ctx context.Context, repo *v1alpha1.GitRepo, _ client.Patch, opts ...interface{}) {
					// check that the commit is the expected one
					if repo.Status.WebhookCommit != expectedCommit {
						t.Errorf("expecting gitrepo webhook commit %s, got %s", expectedCommit, repo.Status.WebhookCommit)
					}
					if repo.Spec.PollingInterval.Duration != time.Hour {
						t.Errorf("expecting gitrepo polling interval 1h, got %s", repo.Spec.PollingInterval.Duration)
					}
				},
			).Times(1)
		}

		w := &Webhook{
			client:    mockClient,
			namespace: "default",
		}

		// we set only the values that we're going to use in the push event to make things simple
		jsonBody := []byte(fmt.Sprintf(`
		{
		  "ref":"refs/heads/main",
		  "after":"%s",
		  "repository":{
			"html_url":"https://github.com/example/repo"
		  }
		}`, expectedCommit))

		// Request creation
		req, err := http.NewRequest(http.MethodPost, "/", bytes.NewReader(jsonBody))
		if err != nil {
			t.Fatalf("Failed to create HTTP request: %v", err)
		}
		req.Header.Set("X-Github-Event", "push")
		// calculate the value to store in the X-Hub-Signature header
		mac := hmac.New(sha1.New, []byte("supersecretvalue"))
		_, _ = mac.Write(jsonBody)
		expectedMAC := hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-Hub-Signature", fmt.Sprintf("sha1=%s", expectedMAC))

		// request execution
		rr := httptest.NewRecorder()
		w.ServeHTTP(rr, req)

		// Verify the response status code is correct
		if status := rr.Code; status != tt.expectedResCode {
			t.Errorf("handler returned wrong status code: got %v want %v", status, tt.expectedResCode)
		}

		// Verify the response message is correct
		if tt.expectedResCode == http.StatusOK {
			expectedResponse := "succeeded"
			if rr.Body.String() != expectedResponse {
				t.Errorf("handler returned unexpected body: got %v want %v", rr.Body, expectedResponse)
			}
		}
	}
}

func TestErrorReadingRequest(t *testing.T) {
	ctlr := gomock.NewController(t)
	mockClient := mocks.NewMockClient(ctlr)
	w := &Webhook{
		client:    mockClient,
		namespace: "default",
	}
	testRequest := httptest.NewRequest(http.MethodPost, "/something", errReader(0))
	rr := httptest.NewRecorder()
	w.ServeHTTP(rr, testRequest)

	// Verify the response status code is correct
	if status := rr.Code; status != http.StatusInternalServerError {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusInternalServerError)
	}
}
