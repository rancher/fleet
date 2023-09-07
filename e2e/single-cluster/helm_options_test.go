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
		When("is false", func() {
			bundleName := "helm-options-disabledns-helm-disable-dns-not-set"
			It("enables DNS when invoking helm", func() {
				Eventually(func() string {
					out, _ := k.Get("bundle", bundleName, `-o=jsonpath={.status.conditions[?(@.type=="Ready")].status}`)
					return out
				}).Should(Equal("True"))
			})
		})
		When("is true", func() {
			bundleName := "helm-options-disabledns-helm-disable-dns-set"
			It("does not enable DNS when invoking helm", func() {
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
		When("is false", func() {
			bundleName := "helm-options-skip-schema-val-helm-schemas-not-set"
			It("fails when schema validation does not pass", func() {
				Eventually(func() string {
					out, _ := k.Get("bundle", bundleName, `-o=jsonpath='{.status.conditions[?(@.type=="Ready")].message}'`)
					return out
				}).Should(ContainSubstring("values don't meet the specifications of the schema"))
			})
		})
		When("is true", func() {
			bundleName := "helm-options-skip-schema-val-helm-schemas-set"
			It("completely skips the schema validation", func() {
				Eventually(func() string {
					out, _ := k.Get("bundle", bundleName, `-o=jsonpath={.status.conditions[?(@.type=="Ready")].status}`)
					return out
				}).Should(Equal("True"))
			})
		})
	})
})
