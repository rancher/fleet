package installation_test

import (
	"os"
	"os/exec"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/matchers"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

var (
	agentMode string
	kd        kubectl.Command
	setupCmd  *exec.Cmd
)

var _ = Describe("Fleet installation with TLS agent modes", func() {
	BeforeEach(func() {
		kd = env.Kubectl.Context(env.Downstream)
	})

	JustBeforeEach(func() {
		cmd := exec.Command(
			"helm",
			"--kube-context",
			"k3d-downstream",
			"uninstall",
			"fleet-agent",
			"-n",
			"cattle-fleet-system",
			"--wait",
		)
		_ = cmd.Run() // Ignore errors, Fleet might not be installed

		err := os.Setenv("FORCE_EMPTY_AGENT_CA", "yes")
		Expect(err).ToNot(HaveOccurred())
		err = os.Setenv("FORCE_API_SERVER_URL", "https://google.com")
		Expect(err).ToNot(HaveOccurred())

		err = os.Setenv("AGENT_TLS_MODE", agentMode)
		Expect(err).ToNot(HaveOccurred())

		go func() {
			setupCmd = exec.Command("../../../dev/setup-fleet-downstream")
			_ = setupCmd.Run()
		}()
	})

	Context("with non-strict agent TLS mode", func() {
		When("fetching fleet-agent-register logs", func() {
			BeforeEach(func() {
				agentMode = "system-store"
			})

			It("reaches the server without cert issues", func() {
				Eventually(func() bool {
					logs, err := kd.Namespace("cattle-fleet-system").Logs(
						"-l",
						"app=fleet-agent",
						"-c",
						"fleet-agent-register",
						"--tail=-1",
					)
					if err != nil {
						return false
					}

					regexMatcher := matchers.MatchRegexpMatcher{
						Regexp: "Failed to register agent.*could not find the requested resource",
					}
					reachesServerWithoutCertIssue, err := regexMatcher.Match(logs)
					if err != nil {
						return false
					}

					return reachesServerWithoutCertIssue
				}).Should(BeTrue())
			})
		})
	})

	Context("with strict agent TLS mode", func() {
		When("fetching fleet-agent-register logs", func() {
			BeforeEach(func() {
				agentMode = "strict"
			})

			It("cannot reach the server because the cert is signed by an unknown authority", func() {
				Eventually(func() bool {
					logs, err := kd.Namespace("cattle-fleet-system").Logs(
						"-l",
						"app=fleet-agent",
						"-c",
						"fleet-agent-register",
						"--tail=-1",
					)
					if err != nil {
						return false
					}

					regexMatcher := matchers.MatchRegexpMatcher{
						Regexp: "Failed to register agent.*signed by unknown authority",
					}
					reachesServerWithoutCertIssue, err := regexMatcher.Match(logs)
					if err != nil {
						return false
					}

					return reachesServerWithoutCertIssue
				}).Should(BeTrue())
			})
		})
	})
})
