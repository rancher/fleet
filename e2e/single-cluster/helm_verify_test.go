package examples_test

import (
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Helm Chart Values", func() {
	var (
		asset string
		k     kubectl.Command
	)
	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
	})

	JustBeforeEach(func() {
		out, err := k.Apply("-f", testenv.AssetPath(asset))
		Expect(err).ToNot(HaveOccurred(), out)
	})

	When("helm chart validates inputs", func() {
		BeforeEach(func() {
			asset = "helm-verify.yaml"
		})

		It("replaces cluster label in values and valuesFiles", func() {
			Eventually(func() string {
				out, _ := k.Namespace("default").Get("configmaps")

				return out
			}).Should(ContainSubstring("app-config"))
			out, _ := k.Namespace("default").Get("configmap", "app-config", "-o", "jsonpath={.data}")
			Expect(out).Should(SatisfyAll(
				ContainSubstring(`"name":"local"`),
				ContainSubstring(`"url":"local"`),
			))
		})
	})
})
