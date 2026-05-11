package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/webhook"
)

// webhookTestRepo is a unique URL that won't match any GitRepo created by other tests.
const webhookTestRepo = "https://github.com/rancher/fleet-webhook-integration-test"

// sendGitHubPush fires a simulated GitHub push event for the given commit against
// the integration test's k8sClient (backed by the envtest API server).
func sendGitHubPush(commit string) {
	w, err := webhook.New(gitRepoNamespace, k8sClient)
	Expect(err).ToNot(HaveOccurred())

	body := []byte(`{"ref":"refs/heads/main","after":"` + commit +
		`","repository":{"html_url":"` + webhookTestRepo + `"}}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", bytes.NewReader(body))
	Expect(err).ToNot(HaveOccurred())
	req.Header.Set("X-GitHub-Event", "push")

	rr := httptest.NewRecorder()
	w.ServeHTTP(rr, req)
	Expect(rr.Code).To(Equal(http.StatusOK))
}

// These tests exercise the webhook HTTP handler against the real envtest API server,
// which enforces the status subresource restriction: a Status().Patch() silently drops
// any spec fields included in the patch.  Only a separate client.Patch() can persist
// spec changes, so the correct two-patch approach is the only way to pass these tests.
var _ = Describe("Webhook handler", func() {
	var gitRepo v1alpha1.GitRepo

	BeforeEach(func() {
		gitRepo = v1alpha1.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "webhook-handler",
				Namespace: gitRepoNamespace,
			},
			Spec: v1alpha1.GitRepoSpec{
				Repo: webhookTestRepo,
			},
		}
	})

	JustBeforeEach(func() {
		Expect(k8sClient.Create(ctx, &gitRepo)).To(Succeed())
	})

	AfterEach(func() {
		waitDeleteGitrepo(gitRepo)
	})

	When("no PollingInterval is configured", func() {
		const pushCommit = "aaaa1111bbbb2222cccc3333dddd4444eeee5555"

		It("sets WebhookCommit and PollingInterval to 1h", func() {
			sendGitHubPush(pushCommit)

			var updated v1alpha1.GitRepo
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: gitRepo.Name, Namespace: gitRepo.Namespace,
			}, &updated)).To(Succeed())

			Expect(updated.Status.WebhookCommit).To(Equal(pushCommit))
			Expect(updated.Spec.PollingInterval).ToNot(BeNil())
			Expect(updated.Spec.PollingInterval.Duration).To(Equal(time.Hour))
		})
	})

	When("PollingInterval is already configured", func() {
		const pushCommit = "bbbb2222cccc3333dddd4444eeee5555ffff6666"
		const existingInterval = 24 * time.Hour

		BeforeEach(func() {
			gitRepo.Spec.PollingInterval = &metav1.Duration{Duration: existingInterval}
		})

		It("sets WebhookCommit without overwriting PollingInterval", func() {
			sendGitHubPush(pushCommit)

			var updated v1alpha1.GitRepo
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: gitRepo.Name, Namespace: gitRepo.Namespace,
			}, &updated)).To(Succeed())

			Expect(updated.Status.WebhookCommit).To(Equal(pushCommit))
			Expect(updated.Spec.PollingInterval).ToNot(BeNil())
			Expect(updated.Spec.PollingInterval.Duration).To(Equal(existingInterval))
		})
	})
})
