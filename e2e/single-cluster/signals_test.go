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
			out, err := k.Run("exec", "deploy/fleet-agent", "--", "kill", "1")
			Expect(err).ToNot(HaveOccurred(), out)
		})

		It("exits gracefully", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get("pod", "-l", "app=fleet-agent", "-o", "jsonpath={.items[0].status.containerStatuses[0].lastState.terminated}")
				g.Expect(err).ToNot(HaveOccurred())

				var state containerLastTerminatedState
				err = json.Unmarshal([]byte(out), &state)
				g.Expect(err).ToNot(HaveOccurred())

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
			pod, err := k.Get("pod", "-l", labels, "-o", "jsonpath={.items[0].metadata.name}")
			Expect(err).ToNot(HaveOccurred(), pod)

			out, err := k.Run("exec", pod, "--", "kill", "1")
			Expect(err).ToNot(HaveOccurred(), out)
		})

		It("exits gracefully", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get(
					"pod",
					"-l", labels,
					"-o", `jsonpath={.items[0].status.containerStatuses[?(@.name=="fleet-controller")].lastState.terminated}`,
				)
				g.Expect(err).ToNot(HaveOccurred())

				var state containerLastTerminatedState
				err = json.Unmarshal([]byte(out), &state)
				g.Expect(err).ToNot(HaveOccurred(), out)

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
			pod, err := k.Get("pod", "-l", labels, "-o", "jsonpath={.items[0].metadata.name}")
			Expect(err).ToNot(HaveOccurred(), pod)

			out, err := k.Run("exec", pod, "--", "kill", "1")
			Expect(err).ToNot(HaveOccurred(), out)
		})

		It("exits gracefully", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get(
					"pod",
					"-l", labels,
					"-o", "jsonpath={.items[0].status.containerStatuses[0].lastState.terminated}",
				)
				g.Expect(err).ToNot(HaveOccurred())

				var state containerLastTerminatedState
				err = json.Unmarshal([]byte(out), &state)
				g.Expect(err).ToNot(HaveOccurred())

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
			pod, err := k.Get("pod", "-l", labels, "-o", "jsonpath={.items[0].metadata.name}")
			Expect(err).ToNot(HaveOccurred(), pod)

			out, err := k.Run("exec", pod, "--", "kill", "1")
			Expect(err).ToNot(HaveOccurred(), out)
		})

		It("exits gracefully", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get(
					"pod",
					"-l", labels,
					"-o", "jsonpath={.items[0].status.containerStatuses[0].lastState.terminated}",
				)
				g.Expect(err).ToNot(HaveOccurred())

				var state containerLastTerminatedState
				err = json.Unmarshal([]byte(out), &state)
				g.Expect(err).ToNot(HaveOccurred())

				g.Expect(state.ExitCode).To(Equal(0))
				g.Expect(state.Reason).To(Equal("Completed"))
			}).Should(Succeed())
		})
	})
})
