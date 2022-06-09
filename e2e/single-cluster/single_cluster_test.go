package examples_test

import (
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
					out, _ := env.Kubectl.Namespace("fleet-helm-example").Get("pods")
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
					out, _ := env.Kubectl.Namespace("fleet-manifest-example").Get("pods")
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
					out, _ := env.Kubectl.Namespace("fleet-kustomize-example").Get("pods")
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
					out, _ := env.Kubectl.Namespace("fleet-helm-kustomize-example").Get("pods")
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
					out, _ := env.Kubectl.Namespace("fleet-multi-chart-helm-example").Get("pods")
					return out
				}, testenv.Timeout).Should(SatisfyAll(ContainSubstring("frontend-"), ContainSubstring("redis-")))
				Eventually(func() string {
					out, _ := env.Kubectl.Namespace("cattle-monitoring-system").Get("deployments")
					return out
				}, testenv.Timeout).Should(ContainSubstring("rancher-monitoring-"))
			})
		})
	})
})
