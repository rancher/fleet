package examples_test

import (
	"strings"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SingleCluster", func() {
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
		Context("containing a helm chart", func() {
			BeforeEach(func() {
				asset = "single-cluster/helm.yaml"
			})

			It("deploys the helm chart", func() {
				Eventually(func() string {
					out, _ := k.Namespace("fleet-helm-example").Get("pods")
					return out
				}, testenv.Timeout).Should(ContainSubstring("frontend-"))
			})
		})

		Context("containing a manifest", func() {
			BeforeEach(func() {
				asset = "single-cluster/manifests.yaml"
			})

			It("deploys the manifest", func() {
				Eventually(func() string {
					out, _ := k.Namespace("fleet-manifest-example").Get("pods")
					return out
				}, testenv.Timeout).Should(SatisfyAll(ContainSubstring("frontend-"), ContainSubstring("redis-")))
			})
		})

		Context("containing a kustomize manifest", func() {
			BeforeEach(func() {
				asset = "single-cluster/kustomize.yaml"
			})

			It("runs kustomize and deploys the manifest", func() {
				Eventually(func() string {
					out, _ := k.Namespace("fleet-kustomize-example").Get("pods")
					return out
				}, testenv.Timeout).Should(ContainSubstring("frontend-"))
			})
		})

		Context("containing an kustomized helm chart", func() {
			BeforeEach(func() {
				asset = "single-cluster/helm-kustomize.yaml"
			})

			It("deploys the helm chart", func() {
				Eventually(func() string {
					out, _ := k.Namespace("fleet-helm-kustomize-example").Get("pods")
					return out
				}, testenv.Timeout).Should(ContainSubstring("frontend-"))
			})
		})

		Context("containing multiple helm charts", func() {
			BeforeEach(func() {
				asset = "single-cluster/helm-multi-chart.yaml"
			})

			It("deploys all the helm charts", func() {
				Eventually(func() string {
					out, _ := k.Namespace("fleet-local").Get("bundles")
					return out
				}, testenv.Timeout).Should(SatisfyAll(
					ContainSubstring("helm-single-cluster-helm-multi-chart-guestbook"),
					ContainSubstring("helm-single-cluster-helm-multi-chart-rancher-monitoring"),
				))

				Eventually(func() string {
					out, _ := k.Get("bundledeployments", "-A")
					return out
				}, testenv.Timeout).Should(SatisfyAll(
					ContainSubstring("helm-single-cluster-helm-multi-chart-guestbook"),
					ContainSubstring("helm-single-cluster-helm-multi-chart-rancher-monitoring"),
				))

				Eventually(func() string {
					out, _ := k.Namespace("fleet-multi-chart-helm-example").Get("deployments")
					return out
				}, testenv.Timeout).Should(SatisfyAll(ContainSubstring("frontend"), ContainSubstring("redis-")))
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
				}, testenv.Timeout).Should(SatisfyAll(
					ContainSubstring("multiple-paths-single-cluster-manifests"),
					ContainSubstring("multiple-paths-single-cluster-helm"),
				))

				out, _ := k.Namespace("fleet-local").Get("bundles",
					"-l", "fleet.cattle.io/repo-name=multiple-paths",
					`-o=jsonpath={.items[*].metadata.name}`)
				Expect(strings.Split(out, " ")).To(HaveLen(2))

				Eventually(func() string {
					out, _ := k.Get("bundledeployments", "-A")
					return out
				}, testenv.Timeout).Should(SatisfyAll(
					ContainSubstring("multiple-paths-single-cluster-manifests"),
					ContainSubstring("multiple-paths-single-cluster-helm"),
				))

				Eventually(func() string {
					out, _ := k.Namespace("fleet-manifest-example").Get("deployments")
					return out
				}, testenv.Timeout).Should(SatisfyAll(ContainSubstring("frontend"), ContainSubstring("redis-")))

				Eventually(func() string {
					out, _ := k.Namespace("fleet-helm-example").Get("deployments")
					return out
				}, testenv.Timeout).Should(SatisfyAll(ContainSubstring("frontend"), ContainSubstring("redis-")))
			})
		})
	})
})
