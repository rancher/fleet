package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/gitjob/e2e/testenv"
	"github.com/rancher/gitjob/e2e/testenv/kubectl"
)

var _ = Describe("Gitjob Examples", func() {
	var (
		asset string
		k     kubectl.Command
	)

	BeforeEach(func() {
		k = env.Kubectl
	})

	JustBeforeEach(func() {
		out, err := k.Apply("-f", testenv.AssetPath(asset))
		Expect(err).ToNot(HaveOccurred(), out)
	})

	AfterEach(func() {
		out, err := k.Delete("-f", testenv.AssetPath(asset))
		Expect(err).ToNot(HaveOccurred(), out)

		out, err = k.Delete("deployment", "nginx-deployment")
		Expect(err).ToNot(HaveOccurred(), out)
	})

	When("creating a gitjob resource", func() {
		Context("referencing a github repo with a deployment", func() {
			BeforeEach(func() {
				asset = "gitjob.yaml"
			})

			It("creates the deployment", func() {
				Eventually(func() string {
					out, _ := k.Get("pods")
					return out
				}, testenv.Timeout).Should(ContainSubstring("nginx-"))
			})
		})
	})
})
