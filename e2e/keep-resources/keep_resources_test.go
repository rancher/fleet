package examples_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

var _ = Describe("Keep resources", func() {
	var (
		asset     string
		k         kubectl.Command
		namespace string
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
	})

	JustBeforeEach(func() {
		out, err := k.Apply("-f", testenv.AssetPath(asset))
		Expect(err).ToNot(HaveOccurred(), out)
		Eventually(func() string {
			out, _ := k.Namespace(namespace).Get("pods")
			return out
		}).Should(ContainSubstring("frontend-"))
	})

	When("GitRepo does not contain keepResources", func() {
		BeforeEach(func() {
			asset = "keep-resources/do-not-keep"
			namespace = "do-not-keep-resources"
		})

		It("resources are deleted when GitRepo is deleted", func() {
			out, err := k.Delete("-f", testenv.AssetPath(asset))
			Expect(err).ToNot(HaveOccurred(), out)

			Eventually(func() string {
				out, _ := k.Namespace(namespace).Get("deployments", "frontend")
				return out
			}).Should(ContainSubstring("Error from server (NotFound)"))
		})
	})

	When("GitRepo has keepResources set to true", func() {
		BeforeEach(func() {
			asset = "keep-resources/keep"
			namespace = "keep-resources"
		})

		It("resources are not deleted when GitRepo is deleted", func() {
			out, err := k.Delete("-f", testenv.AssetPath(asset))
			Expect(err).ToNot(HaveOccurred(), out)

			By("checking resources are not deleted and contains helm.sh/resource-policy annotation")
			Eventually(func() string {
				out, _ := k.Namespace(namespace).Get("deployments", "frontend", "-o", "yaml")
				return out
			}).Should(SatisfyAll(
				Not(ContainSubstring("Error from server (NotFound)")),
				ContainSubstring("helm.sh/resource-policy: keep"),
			))
		})
	})
})
