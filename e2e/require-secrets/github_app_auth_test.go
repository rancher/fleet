package require_secrets

// These test cases rely on an external git server, hence they cannot be run locally nor against PRs.
// For tests relying on an internal git server, see `e2e/single-cluster`.

import (
	"os"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Github App auth tests", func() {
	var (
		k kubectl.Command
	)

	When("creating a GitRepo using a Github App for auth", func() {
		BeforeEach(func() {
			k = env.Kubectl.Namespace(env.Namespace)

			out, err := k.Create(
				"secret", "generic", "git-auth",
				"--from-literal=github_app_id="+os.Getenv("GITHUB_APP_ID"),
				"--from-literal=github_app_installation_id="+os.Getenv("GITHUB_APP_INSTALLATION_ID"),
				"--from-literal=github_app_private_key="+os.Getenv("GITHUB_APP_PRIVATE_KEY"),
			)
			Expect(err).ToNot(HaveOccurred(), out)

			err = testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo_with_auth.yaml"), struct {
				Repo            string
				Branch          string
				PollingInterval string
			}{
				"https://github.com/fleetrepoci/test",
				"master",
				"15s", // default
			})
			Expect(err).ToNot(HaveOccurred(), out)

			DeferCleanup(func() {
				_, _ = k.Delete("gitrepo", "gitrepo-test")
				_, _ = k.Delete("secret", "git-auth")
			})
		})

		It("deploys the workload", func() {
			By("creating the expected pod")
			Eventually(func(g Gomega) {
				out, err := k.Namespace("default").Get("pods", "-l", "app=sleeper")
				g.Expect(err).ToNot(HaveOccurred(), out)
				g.Expect(out).To(ContainSubstring("sleeper-"))
			}).Should(Succeed())
		})
	})
})
