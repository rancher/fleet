package webhook

import (
	"bytes"
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubectl/pkg/scheme"

	"github.com/go-playground/webhooks/v6/azuredevops"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"gopkg.in/go-playground/webhooks.v5/github"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	cfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"net/http"
	"net/http/httptest"
	"testing"

	"gotest.tools/assert"
)

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
	err := v1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	client := cfake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(gitRepo).WithStatusSubresource(gitRepo).Build()
	w := &Webhook{client: client}
	w.azureDevops, _ = azuredevops.New()
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
	if updatedGitRepo.Status.Commit != commit {
		t.Errorf("expected commit %v, but got %v", commit, updatedGitRepo.Status.Commit)
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
	err := v1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Errorf("unexpected error %v", err)
	}
	client := cfake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(gitRepo).WithStatusSubresource(gitRepo).Build()
	w := &Webhook{client: client}
	w.azureDevops, _ = azuredevops.New()
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
	if updatedGitRepo.Status.Commit != commit {
		t.Errorf("expected commit %v, but got %v", commit, updatedGitRepo.Status.Commit)
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
	err := v1alpha1.AddToScheme(sch)
	if err != nil {
		t.Fatalf("unable to add to scheme: %v", err)
	}
	client := cfake.NewClientBuilder().WithScheme(sch).WithRuntimeObjects(gitRepo).Build()

	// Webhook initialisation
	w := &Webhook{
		client:    client,
		namespace: "default",
	}

	w.github, _ = github.New(github.Options.Secret(""))

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
