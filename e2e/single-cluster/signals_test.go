package singlecluster_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

type containerLastTerminatedState struct {
	ExitCode int    `json:"exitCode"`
	Reason   string `json:"reason"`
}

// parseTerminatedState parses the JSON output from kubectl jsonpath query.
// Returns nil if the pod hasn't terminated yet (empty or null JSON output).
func parseTerminatedState(jsonOutput string) (*containerLastTerminatedState, error) {
	if jsonOutput == "" || jsonOutput == "{}" {
		return nil, nil
	}

	var state containerLastTerminatedState
	err := json.Unmarshal([]byte(jsonOutput), &state)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

var _ = Describe("Shuts down gracefully", func() {
	var (
		k      kubectl.Command
		ns     string
		labels string
	)

	When("the agent receives SIGTERM", func() {
		BeforeEach(func() {
			ns = "cattle-fleet-local-system"
			k = env.Kubectl.Namespace(ns)
		})

		JustBeforeEach(func() {
			// Wait for deployment to exist and be ready before sending SIGTERM
			Eventually(func(g Gomega) {
				out, err := k.Get("deployment", "fleet-agent", "-o", "jsonpath={.status.readyReplicas}")
				g.Expect(err).ToNot(HaveOccurred(), "deployment should exist")
				g.Expect(out).ToNot(BeEmpty(), "deployment should have ready replicas")
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				out, err := k.Run("exec", "deploy/fleet-agent", "--", "kill", "1")
				g.Expect(err).ToNot(HaveOccurred(), out)
			}).Should(Succeed())
		})

		It("exits gracefully", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get("pod", "-l", "app=fleet-agent", "-o", "jsonpath={.items[0].status.containerStatuses[0].lastState.terminated}")
				g.Expect(err).ToNot(HaveOccurred())

				state, err := parseTerminatedState(out)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(state).NotTo(BeNil(), "pod should have terminated")

				g.Expect(state.ExitCode).To(Equal(0))
				g.Expect(state.Reason).To(Equal("Completed"))
			}).Should(Succeed())
		})
	})

	// Note: when dealing with sharded deployments, running `kubectl exec <deployment_name>` does not resolve to the
	// right deployment, as if name resolution were prefix-based. This causes checks against pods' container states
	// to fail, because changes would have been applied to a different deployment.
	// Therefore, we explicitly compute pod names based on labels.
	When("the fleet-controller deployment receives SIGTERM", func() {
		BeforeEach(func() {
			ns = "cattle-fleet-system"
			k = env.Kubectl.Namespace(ns)
			labels = "app=fleet-controller,fleet.cattle.io/shard-default=true"
		})

		JustBeforeEach(func() {
			// Wait for pod to exist and be ready before sending SIGTERM
			Eventually(func(g Gomega) {
				pods, err := k.Get("pod", "-l", labels, "-o", "jsonpath={.items[*].status.phase}")
				g.Expect(err).ToNot(HaveOccurred(), "pods should exist")
				g.Expect(pods).To(ContainSubstring("Running"), "at least one pod should be running")
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				pod, err := k.Get("pod", "-l", labels, "-o", "jsonpath={.items[0].metadata.name}")
				g.Expect(err).ToNot(HaveOccurred(), pod)

				out, err := k.Run("exec", pod, "--", "kill", "1")
				g.Expect(err).ToNot(HaveOccurred(), out)
			}).Should(Succeed())
		})

		It("exits gracefully", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get(
					"pod",
					"-l", labels,
					"-o", `jsonpath={.items[0].status.containerStatuses[?(@.name=="fleet-controller")].lastState.terminated}`,
				)
				g.Expect(err).ToNot(HaveOccurred())

				state, err := parseTerminatedState(out)
				g.Expect(err).ToNot(HaveOccurred(), out)
				g.Expect(state).NotTo(BeNil(), "pod should have terminated")

				g.Expect(state.ExitCode).To(Equal(0))
				g.Expect(state.Reason).To(Equal("Completed"))
			}).Should(Succeed())
		})
	})

	When("the gitjob deployment receives SIGTERM", func() {
		BeforeEach(func() {
			ns = "cattle-fleet-system"
			k = env.Kubectl.Namespace(ns)
			labels = "app=gitjob,fleet.cattle.io/shard-default=true"
		})

		JustBeforeEach(func() {
			// Wait for pod to exist and be ready before sending SIGTERM
			Eventually(func(g Gomega) {
				pods, err := k.Get("pod", "-l", labels, "-o", "jsonpath={.items[*].status.phase}")
				g.Expect(err).ToNot(HaveOccurred(), "pods should exist")
				g.Expect(pods).To(ContainSubstring("Running"), "at least one pod should be running")
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				pod, err := k.Get("pod", "-l", labels, "-o", "jsonpath={.items[0].metadata.name}")
				g.Expect(err).ToNot(HaveOccurred(), pod)

				out, err := k.Run("exec", pod, "--", "kill", "1")
				g.Expect(err).ToNot(HaveOccurred(), out)
			}).Should(Succeed())
		})

		It("exits gracefully", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get(
					"pod",
					"-l", labels,
					"-o", "jsonpath={.items[0].status.containerStatuses[0].lastState.terminated}",
				)
				g.Expect(err).ToNot(HaveOccurred())

				state, err := parseTerminatedState(out)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(state).NotTo(BeNil(), "pod should have terminated")

				g.Expect(state.ExitCode).To(Equal(0))
				g.Expect(state.Reason).To(Equal("Completed"))
			}).Should(Succeed())
		})
	})

	When("the helmops deployment receives SIGTERM", func() {
		BeforeEach(func() {
			ns = "cattle-fleet-system"
			k = env.Kubectl.Namespace(ns)
			labels = "app=helmops,fleet.cattle.io/shard-default=true"
		})

		JustBeforeEach(func() {
			// Wait for pod to exist and be ready before sending SIGTERM
			Eventually(func(g Gomega) {
				pods, err := k.Get("pod", "-l", labels, "-o", "jsonpath={.items[*].status.phase}")
				g.Expect(err).ToNot(HaveOccurred(), "pods should exist")
				g.Expect(pods).To(ContainSubstring("Running"), "at least one pod should be running")
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				pod, err := k.Get("pod", "-l", labels, "-o", "jsonpath={.items[0].metadata.name}")
				g.Expect(err).ToNot(HaveOccurred(), pod)

				out, err := k.Run("exec", pod, "--", "kill", "1")
				g.Expect(err).ToNot(HaveOccurred(), out)
			}).Should(Succeed())
		})

		It("exits gracefully", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get(
					"pod",
					"-l", labels,
					"-o", "jsonpath={.items[0].status.containerStatuses[0].lastState.terminated}",
				)
				g.Expect(err).ToNot(HaveOccurred())

				state, err := parseTerminatedState(out)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(state).NotTo(BeNil(), "pod should have terminated")

				g.Expect(state.ExitCode).To(Equal(0))
				g.Expect(state.Reason).To(Equal("Completed"))
			}).Should(Succeed())
		})
	})
})
