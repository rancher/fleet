package examples_test

import (
	"strings"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These tests use the examples from https://github.com/rancher/fleet-examples/tree/master/single-cluster
var _ = Describe("Single Cluster Examples", func() {
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

	When("creating a gitrepo resource", func() {
		Context("containing a public oci based helm chart", func() {
			BeforeEach(func() {
				asset = "single-cluster/helm-oci.yaml"
			})

			It("deploys the helm chart", func() {
				Eventually(func() string {
					out, _ := k.Namespace("fleet-helm-oci-example").Get("pods")
					return out
				}).Should(ContainSubstring("frontend-"))
			})
		})

		Context("containing no kustomized helm chart but uses an invalid name for kustomize", func() {
			BeforeEach(func() {
				asset = "single-cluster/helm-kustomize-disabled.yaml"
			})

			It("deploys the helm chart", func() {
				Eventually(func() string {
					out, _ := k.Namespace("helm-kustomize-disabled").Get("configmap", "-o", "yaml")
					return out
				}).Should(ContainSubstring("name: helm-kustomize-disabled"))
			})
		})

		Context("containing multiple paths", func() {
			BeforeEach(func() {
				asset = "single-cluster/multiple-paths.yaml"
			})

			It("deploys bundles from all the paths", func() {
				Eventually(func() string {
					out, _ := k.Namespace("fleet-local").Get("bundles")
					return out
				}).Should(SatisfyAll(
					ContainSubstring("multiple-paths-multiple-paths-config"),
					ContainSubstring("multiple-paths-multiple-paths-service"),
				))

				out, _ := k.Namespace("fleet-local").Get("bundles",
					"-l", "fleet.cattle.io/repo-name=multiple-paths",
					`-o=jsonpath={.items[*].metadata.name}`)
				Expect(strings.Split(out, " ")).To(HaveLen(2))

				Eventually(func() string {
					out, _ := k.Get("bundledeployments", "-A")
					return out
				}).Should(SatisfyAll(
					ContainSubstring("multiple-paths-multiple-paths-config"),
					ContainSubstring("multiple-paths-multiple-paths-service"),
				))

				Eventually(func() string {
					out, _ := k.Namespace("test-fleet-mp-config").Get("configmaps")
					return out
				}).Should(ContainSubstring("mp-app-config"))

				Eventually(func() string {
					out, _ := k.Namespace("test-fleet-mp-service").Get("services")
					return out
				}).Should(ContainSubstring("mp-app-service"))
			})
		})
	})
})
