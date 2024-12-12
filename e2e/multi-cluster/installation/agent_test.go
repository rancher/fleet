package installation_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/matchers"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

var (
	agentMode string
	kd        kubectl.Command
)

var _ = Describe("Fleet installation with TLS agent modes", func() {
	BeforeEach(func() {
		kd = env.Kubectl.Context(env.Downstream)
	})

	JustBeforeEach(func() {
		out, err := ku.Patch(
			"configmap",
			"fleet-controller",
			"-n",
			"cattle-fleet-system",
			"--type=merge",
			"-p",
			// Agent check-in interval cannot be 0. Any other value will do here.
			fmt.Sprintf(
				`{"data":{"config":"{\"apiServerURL\": \"https://google.com\", \"apiServerCA\": \"\", \"agentTLSMode\": \"%s\", \"agentCheckinInterval\": \"1m\"}"}}`,
				agentMode,
			),
		)
		Expect(err).ToNot(HaveOccurred(), string(out))

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
