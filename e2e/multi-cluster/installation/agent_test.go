package installation_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
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
		When("fetching fleet-agent logs", func() {
			BeforeEach(func() {
				agentMode = "system-store"
			})

			It("reaches the server without cert issues", func() {
				Eventually(func(g Gomega) error {
					logs, err := kd.Namespace("cattle-fleet-system").Logs(
						"-l",
						"app=fleet-agent",
						"-c",
						"fleet-agent",
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
		BeforeEach(func() {
			agentMode = "strict"
		})

		When("fetching fleet-agent logs", func() {
			It("cannot reach the server because the cert is signed by an unknown authority", func() {
				Eventually(func(g Gomega) error {
					logs, err := kd.Namespace("cattle-fleet-system").Logs(
						"-l",
						"app=fleet-agent",
						"-c",
						"fleet-agent",
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

var _ = Describe("HelmOps installation with strict TLS mode", func() {
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
		restoreConfig() // prevent interference with other test cases in the suite.
		out, err := ku.Patch(
			"configmap",
			"fleet-controller",
			"-n",
			"cattle-fleet-system",
			"--type=merge",
			"-p",
			fmt.Sprintf(`{"data":{"config":"{\"agentTLSMode\": \"%s\"}"}}`, agentMode),
		)
		Expect(err).ToNot(HaveOccurred(), string(out))

		// Check that the config change has been applied downstream
		type configWithTLSMode struct {
			AgentTLSMode string `json:"agentTLSMode"`
		}
		Eventually(func(g Gomega) {
			data, err := kd.Namespace("cattle-fleet-system").Get(
				"configmap",
				"fleet-agent",
				"-o",
				"jsonpath={.data.config}",
			)
			Expect(err).ToNot(HaveOccurred(), data)

			var c configWithTLSMode

			err = json.Unmarshal([]byte(data), &c)
			Expect(err).ToNot(HaveOccurred())

			Expect(c.AgentTLSMode).To(Equal("strict"))
		}).Should(Succeed())
	})

	When("installing a HelmOp", func() {
		name := "basic"
		ns := "fleet-default"

		JustBeforeEach(func() {
			ku = ku.Namespace(ns)
			err := testenv.ApplyTemplate(ku, "../../assets/multi-cluster/helmop.yaml", struct {
				Name                  string
				Namespace             string
				Repo                  string
				Chart                 string
				Version               string
				PollingInterval       time.Duration
				HelmSecretName        string
				InsecureSkipTLSVerify bool
			}{
				name,
				ns,
				"",
				"https://github.com/rancher/fleet/raw/refs/heads/main/integrationtests/cli/assets/helmrepository/config-chart-0.1.0.tgz",
				"0.1.0",
				0,
				"",
				false,
			})
			Expect(err).ToNot(HaveOccurred())
		})

		It("deploys the chart", func() {
			Eventually(func() bool {
				outPods, _ := kd.Get("configmaps")
				return strings.Contains(outPods, "test-simple-chart-config")
			}).Should(BeTrue())
		})

		AfterEach(func() {
			_, err := ku.Delete("helmop", name)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
