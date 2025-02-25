package installation_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var (
	agentMode string
)

var _ = Describe("Fleet installation with TLS agent modes", func() {
	BeforeEach(func() {
		kd = env.Kubectl.Context(env.Downstream)

		_, err := kd.Delete(
			"pod",
			"-n",
			"cattle-fleet-system",
			"-l",
			"app=fleet-agent",
		)
		Expect(err).NotTo(HaveOccurred())
	})

	JustBeforeEach(func() {
		out, err := ku.Patch(
			"configmap",
			"fleet-controller",
			"-n",
			"cattle-fleet-system",
			"--type=merge",
			"-p",
			fmt.Sprintf(
				`{"data":{"config":"{\"apiServerURL\": \"https://google.com\", \"apiServerCA\": \"\", \"agentTLSMode\": \"%s\"}"}}`,
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
				Eventually(func(g Gomega) error {
					logs, err := kd.Namespace("cattle-fleet-system").Logs(
						"-l",
						"app=fleet-agent",
						"-c",
						"fleet-agent-register",
						"--tail=-1",
					)
					if err != nil {
						return err
					}

					g.Expect(logs).To(MatchRegexp("Failed to register agent.*could not find the requested resource"))

					return nil
				}).ShouldNot(HaveOccurred())
			})
		})
	})

	Context("with strict agent TLS mode", func() {
		When("fetching fleet-agent-register logs", func() {
			BeforeEach(func() {
				agentMode = "strict"
			})

			It("cannot reach the server because the cert is signed by an unknown authority", func() {
				Eventually(func(g Gomega) error {
					logs, err := kd.Namespace("cattle-fleet-system").Logs(
						"-l",
						"app=fleet-agent",
						"-c",
						"fleet-agent-register",
						"--tail=-1",
					)
					if err != nil {
						return err
					}

					g.Expect(logs).To(MatchRegexp("Failed to register agent.*signed by unknown authority"))
					if err != nil {
						return err
					}

					return nil
				}).ShouldNot(HaveOccurred())
			})
		})
	})
})
