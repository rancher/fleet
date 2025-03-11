package singlecluster_test

import (
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Helm deploy options", func() {
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

	AfterEach(func() {
		out, err := k.Delete("-f", testenv.AssetPath(asset))
		Expect(err).ToNot(HaveOccurred(), out)
	})

	Describe("DisableDNS", func() {
		BeforeEach(func() {
			asset = "single-cluster/helm-options-disabledns.yaml"
		})
		When("toggling DisableDNS", func() {
			It("honors DisableDNS", func() {
				By("enabling DNS when invoking helm if DisableDNS is false")
				bundleName := "helm-options-disabledns-helm-disable-dns-not-set"
				Eventually(func() string {
					out, _ := k.Get("bundle", bundleName, `-o=jsonpath={.status.conditions[?(@.type=="Ready")].status}`)
					return out
				}).Should(Equal("True"))

				By("not enabling DNS when invoking helm if DisableDNS is true")
				bundleName = "helm-options-disabledns-helm-disable-dns-set"
				Eventually(func() string {
					out, _ := k.Get("bundle", bundleName, `-o=jsonpath='{.status.conditions[?(@.type=="Ready")].message}'`)
					return out
				}).Should(ContainSubstring("DNS is not enabled"))
			})
		})
	})

	Describe("SkipSchemaValidation", func() {
		BeforeEach(func() {
			asset = "single-cluster/helm-options-skip-schema-validation.yaml"
		})
		When("toggling SkipSchemaValidation", func() {
			It("honors SkipSchemaValidation", func() {
				By("enabling SkipSchemaValidation and failing when schema validation does not pass")
				bundleName := "helm-options-skip-schema-val-helm-schemas-not-set"
				Eventually(func() string {
					out, _ := k.Get("bundle", bundleName, `-o=jsonpath='{.status.conditions[?(@.type=="Ready")].message}'`)
					return out
				}).Should(ContainSubstring("values don't meet the specifications of the schema"))

				By("completely skipping schema validation when disabled")
				bundleName = "helm-options-skip-schema-val-helm-schemas-set"
				Eventually(func() string {
					out, _ := k.Get("bundle", bundleName, `-o=jsonpath={.status.conditions[?(@.type=="Ready")].status}`)
					return out
				}).Should(Equal("True"))
			})
		})
	})
})
