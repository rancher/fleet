package require_secrets

import (
	"os"
	"path"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These tests use the examples from https://github.com/rancher/fleet-examples/tree/master/single-cluster
var _ = Describe("Single Cluster Examples", func() {
	var (
		asset  string
		tmpdir string
		k      kubectl.Command
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		tmpdir, _ = os.MkdirTemp("", "fleet-")
	})

	JustBeforeEach(func() {
		gitrepo := path.Join(tmpdir, "gitrepo.yaml")

		err := testenv.Template(gitrepo, testenv.AssetPath(asset), struct {
			Repo       string
			Path       string
			SecretName string
		}{
			"https://github.com/rancher/fleet-test-data",
			"helm-oci-with-auth",
			"helm-oci-secret",
		})
		Expect(err).ToNot(HaveOccurred())

		out, err := k.Apply("-f", gitrepo)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	AfterEach(func() {
		os.RemoveAll(tmpdir)
		out, err := k.Delete("-f", testenv.AssetPath(asset))
		Expect(err).ToNot(HaveOccurred(), out)
	})

	When("creating a gitrepo resource", func() {
		Context("containing a private oci based helm chart", func() {
			BeforeEach(func() {
				asset = "single-cluster/helm-with-auth.yaml"
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
