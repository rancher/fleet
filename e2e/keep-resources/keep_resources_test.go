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

		DeferCleanup(func() {
			// Let's delete the namespace and its resources anyway once done, as this may free up precious
			// resources, especially on CI runners.
			// Redis pods may take over a minute to terminate, hence we skip the wait here.
			_, _ = k.Delete("ns", namespace, "--wait=false")
		})
	})

	JustBeforeEach(func() {
		out, err := k.Apply("-f", testenv.AssetPath(asset))
		Expect(err).ToNot(HaveOccurred(), out)
		Eventually(func() string {
			out, _ := k.Namespace(namespace).Get("configmaps")
			return out
		}).Should(ContainSubstring("app-config"))
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
				out, _ := k.Namespace(namespace).Get("configmap", "app-config")
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

			By("checking helm secrets are deleted")
			Eventually(func() string {
				out, _ := k.Namespace(namespace).Get("secrets", "-l", "owner=helm")
				return out
			}).Should(ContainSubstring("No resources found"))

			By("checking resources are not deleted")
			Eventually(func() string {
				out, _ := k.Namespace(namespace).Get("configmap", "app-config", "-o", "yaml")
				return out
			}).Should(SatisfyAll(
				Not(ContainSubstring("Error from server (NotFound)")),
			))
		})
	})
})
