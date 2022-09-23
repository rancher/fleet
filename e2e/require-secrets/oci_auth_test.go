package require_secrets

import (
	"os"

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
		Context("containing a private oci based helm chart", func() {
			BeforeEach(func() {
				asset = "single-cluster/helm-oci-with-auth.yaml"
				k = env.Kubectl.Namespace(env.Namespace)

				out, err := k.Create(
					"secret", "generic", "helm-oci-secret",
					"--from-literal=username="+os.Getenv("CI_OCI_USERNAME"),
					"--from-literal=password="+os.Getenv("CI_OCI_PASSWORD"),
				)
				Expect(err).ToNot(HaveOccurred(), out)
			})

			AfterEach(func() {
				k = env.Kubectl.Namespace(env.Namespace)

				out, err := k.Delete(
					"secret", "helm-oci-secret",
				)
				Expect(err).ToNot(HaveOccurred(), out)
			})

			It("deploys the helm chart", func() {
				Eventually(func() string {
					out, _ := k.Namespace("fleet-helm-oci-with-auth-example").Get("pods")
					return out
				}).Should(ContainSubstring("frontend-"))
			})
		})
	})
})
