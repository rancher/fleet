package singlecluster_test

import (
	"strings"

	"github.com/asaskevich/govalidator"
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Single Cluster Deployments", func() {
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
					out, _ := k.Namespace("fleet-helm-oci-example").Get("configmaps")
					return out
				}).Should(ContainSubstring("fleet-test-configmap"))
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
		// This test fails from v0.9.1-rc.2 to v0.9.1-rc.6 due to issue:
		// https://github.com/rancher/fleet/issues/2128
		Context("containing multiple deployments", func() {
			BeforeEach(func() {
				asset = "single-cluster/helm-status-check.yaml"
			})

			It("all deployments are ready and status shown is Ready", func() {
				Eventually(func() bool {
					slowtestReady := checkNReplicasAreReady(k, "fleet-helm-example", "slowtest", "2")
					return slowtestReady
				}).Should(Equal(true))

				Eventually(func() bool {
					return checkBundleReady(k, "fleet-local", "sample-helm-deployment-status")
				}).Should(Equal(true))

				Eventually(func() bool {
					return checkGitRepoReady(k, "fleet-local", "sample")
				}).Should(Equal(true))
			})
		})
	})
})

func checkNReplicasAreReady(k kubectl.Command, namespace, deployment, nreplicas string) bool {
	replicas, _ := k.Namespace(namespace).Get("deployment", deployment, `-o=jsonpath={.status.replicas}`)
	readyReplicas, _ := k.Namespace(namespace).Get("deployment", deployment, `-o=jsonpath={.status.readyReplicas}`)
	return govalidator.IsInt(replicas) && govalidator.IsInt(readyReplicas) && replicas == readyReplicas && replicas == nreplicas
}

func checkBundleReady(k kubectl.Command, namespace, bundle string) bool {
	desiredReady, _ := k.Namespace(namespace).Get("bundle", bundle, `-o=jsonpath={.status.summary.desiredReady}`)
	ready, _ := k.Namespace(namespace).Get("bundle", bundle, `-o=jsonpath={.status.summary.ready}`)
	return govalidator.IsInt(desiredReady) && govalidator.IsInt(ready) && ready == desiredReady
}

func checkGitRepoReady(k kubectl.Command, namespace, gitrepo string) bool {
	desiredReady, _ := k.Namespace(namespace).Get("gitrepo", gitrepo, `-o=jsonpath={.status.summary.desiredReady}`)
	ready, _ := k.Namespace(namespace).Get("gitrepo", gitrepo, `-o=jsonpath={.status.summary.ready}`)
	return govalidator.IsInt(desiredReady) && govalidator.IsInt(ready) && ready == desiredReady
}
