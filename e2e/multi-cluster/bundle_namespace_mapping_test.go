package mc_examples_test

import (
	"os"
	"path"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Bundle Namespace Mapping", func() {
	var (
		k  kubectl.Command
		kd kubectl.Command

		asset     string
		namespace string
		data      any
		tmpdir    string
	)

	BeforeEach(func() {
		k = env.Kubectl.Context(env.Fleet)
		kd = env.Kubectl.Context(env.Downstream)
	})

	JustBeforeEach(func() {
		tmpdir, _ = os.MkdirTemp("", "fleet-")
		output := path.Join(tmpdir, testenv.RandomFilename("manifests.yaml"))
		err := testenv.Template(output, testenv.AssetPath(asset), data)
		Expect(err).ToNot(HaveOccurred())

		out, err := k.Namespace(namespace).Apply("-f", output)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	AfterEach(func() {
		out, err := k.Delete("ns", namespace)
		Expect(err).ToNot(HaveOccurred(), out)
		os.RemoveAll(tmpdir)
	})

	When("creating gitrepo resource in another namespace", func() {
		Context("downstream namespace is allowed", func() {
			BeforeEach(func() {
				namespace = "project1"
				asset = "multi-cluster/bundle-namespace-mapping.yaml"
				data = struct {
					Namespace       string
					TargetNamespace string
				}{namespace, "project1simpleapp"}
			})

			It("deploys to the the downstream cluster", func() {
				Eventually(func() string {
					out, _ := k.Get("bundledeployments", "-A")
					return out
				}).Should(ContainSubstring("simpleapp-bundle-diffs"))
				Eventually(func() string {
					out, _ := kd.Namespace("project1simpleapp").Get("configmaps")
					return out
				}).Should(ContainSubstring("app-config"))
			})
		})

		Context("downstream namespace is denied by gitreporestriction", func() {
			BeforeEach(func() {
				namespace = "project2"
				asset = "multi-cluster/bundle-namespace-mapping.yaml"
				data = struct {
					Namespace       string
					TargetNamespace string
				}{namespace, "denythisnamespace"}
			})

			It("denies deployment to downstream cluster", func() {
				Eventually(func() string {
					out, _ := k.Namespace(namespace).Get("gitrepo", "simpleapp",
						"-o=jsonpath={.status.conditions[*].message}",
					)
					return out
				}).Should(ContainSubstring("disallowed targetNamespace denythisnamespace"))
			})
		})
	})
})
