package examples_test

import (
	"encoding/json"

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
		Context("containing a helm chart", func() {
			BeforeEach(func() {
				asset = "single-cluster-examples/helm.yaml"
			})

			It("deploys the helm chart", func() {
				Eventually(func() string {
					out, _ := k.Namespace("fleet-helm-example").Get("pods")
					return out
				}).Should(ContainSubstring("frontend-"))

				By("Checking that labels from gitrepo are present on the bundle", func() {
					out, err := k.Namespace("fleet-local").Get("bundle", "helm-single-cluster-helm",
						`-o=jsonpath={.metadata.labels}`)
					Expect(err).ToNot(HaveOccurred())

					labels := &map[string]string{}
					err = json.Unmarshal([]byte(out), labels)
					Expect(err).ToNot(HaveOccurred())
					Expect(*labels).To(HaveKeyWithValue("test", "me"))
				})
			})
		})

		Context("containing a manifest", func() {
			BeforeEach(func() {
				asset = "single-cluster-examples/manifests.yaml"
			})

			It("deploys the manifest", func() {
				Eventually(func() string {
					out, _ := k.Namespace("fleet-manifest-example").Get("pods")
					return out
				}).Should(SatisfyAll(ContainSubstring("frontend-"), ContainSubstring("redis-")))
			})
		})

		Context("containing a kustomize manifest", func() {
			BeforeEach(func() {
				asset = "single-cluster-examples/kustomize.yaml"
			})

			It("runs kustomize and deploys the manifest", func() {
				Eventually(func() string {
					out, _ := k.Namespace("fleet-kustomize-example").Get("pods")
					return out
				}).Should(ContainSubstring("frontend-"))
			})
		})

		Context("containing an kustomized helm chart", func() {
			BeforeEach(func() {
				asset = "single-cluster-examples/helm-kustomize.yaml"
			})

			It("deploys the helm chart", func() {
				Eventually(func() string {
					out, _ := k.Namespace("fleet-helm-kustomize-example").Get("pods")
					return out
				}).Should(ContainSubstring("frontend-"))
			})
		})

		Context("containing multiple helm charts", func() {
			BeforeEach(func() {
				asset = "single-cluster-examples/helm-multi-chart.yaml"
			})

			It("deploys all the helm charts", func() {
				Eventually(func() string {
					out, _ := k.Namespace("fleet-local").Get("bundles")
					return out
				}).Should(SatisfyAll(
					ContainSubstring("helm-single-cluster-helm-multi-chart-guestbook"),
					ContainSubstring("helm-single-cluster-helm-multi-chart-rancher-mo-"),
				))

				Eventually(func() string {
					out, _ := k.Get("bundledeployments", "-A")
					return out
				}).Should(SatisfyAll(
					ContainSubstring("helm-single-cluster-helm-multi-chart-guestbook"),
					ContainSubstring("helm-single-cluster-helm-multi-chart-rancher-mo-"),
				))

				Eventually(func() string {
					out, _ := k.Namespace("fleet-multi-chart-helm-example").Get("deployments")
					return out
				}).Should(SatisfyAll(ContainSubstring("frontend"), ContainSubstring("redis-")))
			})
		})

	})
})
