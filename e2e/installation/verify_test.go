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
		asset   string
		k       kubectl.Command
		version = "dev"
		// this is the default for fleet standalone
		localAgentNamespace = "cattle-fleet-system"
		agentNamespace      = "cattle-fleet-system"
	)

	BeforeEach(func() {
		k = env.Kubectl.Context(env.Upstream).Namespace(env.Namespace)
		if v, ok := os.LookupEnv("FLEET_VERSION"); ok {
			version = v
		}
		if n, ok := os.LookupEnv("FLEET_LOCAL_AGENT_NAMESPACE"); ok {
			localAgentNamespace = n
		}
		if n, ok := os.LookupEnv("FLEET_AGENT_NAMESPACE"); ok {
			agentNamespace = n
		}
	})

	Context("Verify bundles are deployed", Label("single-cluster"), func() {
		It("finds the original workload", func() {
			out, _ := k.Namespace("simple-example").Get("services")
			Expect(out).To(ContainSubstring("simple-service"))
		})
	})

	Context("Verify bundles are deployed", Label("multi-cluster"), func() {
		It("finds the original workload", func() {
			out, _ := k.Namespace("default").Get("cm")
			Expect(out).To(SatisfyAll(
				ContainSubstring("test-simple-chart-config"),
				ContainSubstring("test-simple-manifest-config"),
			))

			kd := env.Kubectl.Context(env.Downstream)
			out, _ = kd.Namespace("default").Get("cm")
			Expect(out).To(SatisfyAll(
				ContainSubstring("test-simple-chart-config"),
				ContainSubstring("test-simple-manifest-config"),
			))
		})
	})

	Context("Verify agents are updated to new image", func() {
		It("has the expected fleet images", func() {
			Eventually(func() string {
				out, _ := k.Namespace(localAgentNamespace).Get("deployments", "-owide")
				return out
			}).Should(ContainSubstring("rancher/fleet-agent:" + version))
		})

		It("has the expected fleet-agent image in the downstream cluster", Label("multi-cluster"), func() {
			kd := env.Kubectl.Context(env.Downstream)
			Eventually(func() string {
				out, _ := kd.Namespace(agentNamespace).Get("deployments", "-owide")
				return out
			}).Should(ContainSubstring("rancher/fleet-agent:" + version))
		})
	})

	When("Deploying another bundle still works", func() {
		var tmpdir string
		BeforeEach(func() {
			asset = "installation/verify.yaml"
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
			}).Should(ContainSubstring("simple-config"))
		})

	})
})
