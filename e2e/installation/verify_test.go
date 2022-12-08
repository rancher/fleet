package installation_test

import (
	"os"
	"path"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// This runs after an upgrade of fleet to verify workloads are still there and
// new workload can be created
var _ = Describe("Fleet Installation", func() {
	var (
		asset string
		k     kubectl.Command
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
	})

	Context("sanity checks", func() {
		It("finds the original workload", func() {
			out, _ := k.Namespace("bundle-diffs-example").Get("services")
			Expect(out).To(ContainSubstring("app-service"))
		})

	})

	When("Deploying another bundle", func() {
		var tmpdir string
		BeforeEach(func() {
			asset = "simple/gitrepo.yaml"
		})

		JustBeforeEach(func() {
			tmpdir, _ = os.MkdirTemp("", "fleet-")
			gitrepo := path.Join(tmpdir, "gitrepo.yaml")
			err := testenv.Template(gitrepo, testenv.AssetPath(asset), struct {
				Name            string
				TargetNamespace string
			}{
				"testname",
				"testexample",
			})
			Expect(err).ToNot(HaveOccurred())

			out, err := k.Apply("-f", gitrepo)
			Expect(err).ToNot(HaveOccurred(), out)
		})

		AfterEach(func() {
			os.RemoveAll(tmpdir)
		})

		It("creates the new workload", func() {
			Eventually(func() string {
				out, _ := k.Namespace("testexample").Get("configmaps")
				return out
			}).Should(ContainSubstring("app-config"))
		})

	})
})
