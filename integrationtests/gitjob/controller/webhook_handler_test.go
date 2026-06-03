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

	When("a push webhook arrives", func() {
		const pushCommit = "cccc3333dddd4444eeee5555ffff6666aaaa1111"

		It("sets LastWebhookTime to a recent timestamp", func() {
			// metav1.Time stores at second granularity, so truncate before comparing.
			before := metav1.Now().Truncate(time.Second)
			sendGitHubPush(pushCommit)

			Eventually(func(g Gomega) {
				var updated v1alpha1.GitRepo
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: gitRepo.Name, Namespace: gitRepo.Namespace,
				}, &updated)).To(Succeed())
				g.Expect(updated.Status.LastWebhookTime.IsZero()).To(BeFalse())
				g.Expect(updated.Status.LastWebhookTime.Time).To(BeTemporally(">=", before))
			}).Should(Succeed())
		})
	})
})
