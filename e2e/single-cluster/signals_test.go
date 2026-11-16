package singlecluster_test

import (
	"encoding/json"
	"strings"

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
	trimmed := strings.TrimSpace(jsonOutput)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" || trimmed == "<no value>" {
		return nil, nil
	}

	var state containerLastTerminatedState
	err := json.Unmarshal([]byte(trimmed), &state)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

// waitForLeadership waits until the given leader election lease is held by an instance other
// than previousPod. A terminated leader keeps its lease until the lease duration elapses, so
// the replacement pod is only reconciling once it has taken over the lease.
func waitForLeadership(k kubectl.Command, lease, previousPod string) {
	Eventually(func(g Gomega) {
		holder, err := k.Get("lease", lease, "-o", "jsonpath={.spec.holderIdentity}")
		g.Expect(err).ToNot(HaveOccurred(), holder)
		g.Expect(holder).ToNot(BeEmpty())
		g.Expect(holder).ToNot(HavePrefix(previousPod), "new leader should not be the terminated pod")
	}).Should(Succeed())
}

// waitForPodReadyAndTerminate waits for a pod matching the given label selector to be ready,
// then deletes it to trigger graceful pod termination (SIGTERM).
func waitForPodReadyAndTerminate(k kubectl.Command, labels string) string {
	var pod string

	// Wait for pod to exist and be ready before sending SIGTERM
	Eventually(func(g Gomega) {
		pods, err := k.Get("pod", "-l", labels, "-o", "jsonpath={.items[*].status.phase}")
		g.Expect(err).ToNot(HaveOccurred(), "pods should exist")
		g.Expect(pods).To(ContainSubstring("Running"), "at least one pod should be running")
	}).Should(Succeed())

	Eventually(func(g Gomega) {
		name, err := k.Get("pod", "-l", labels, "-o", `jsonpath={.items[?(@.status.phase=="Running")].metadata.name}`)
		g.Expect(err).ToNot(HaveOccurred(), name)
		runningPods := strings.Fields(name)
		g.Expect(runningPods).ToNot(BeEmpty(), name)
		pod = runningPods[0]

		out, err := k.Run("delete", "pod", pod, "--wait=false")
		g.Expect(err).ToNot(HaveOccurred(), out)
	}).Should(Succeed())

	return pod
}

var _ = Describe("Shuts down gracefully", func() {
	var (
		k      kubectl.Command
		ns     string
		labels string
		pod    string
	)

	When("the agent receives SIGTERM", func() {
		BeforeEach(func() {
			ns = "cattle-fleet-local-system"
			k = env.Kubectl.Namespace(ns)
		})

		JustBeforeEach(func() {
			pod = waitForPodReadyAndTerminate(k, "app=fleet-agent")
		})

		It("exits gracefully", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get("pod", pod, "-o", "jsonpath={.status.containerStatuses[0].state.terminated}")
				if err != nil {
					g.Expect(strings.Contains(strings.ToLower(out), "notfound")).To(BeTrue(), out)
					return
				}

				state, err := parseTerminatedState(out)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(state).NotTo(BeNil(), "pod should have terminated")

				g.Expect(state.ExitCode).To(Equal(0))
				g.Expect(state.Reason).To(Equal("Completed"))
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				pods, err := k.Get("pod", "-l", "app=fleet-agent", "-o", "jsonpath={.items[*].status.phase}")
				g.Expect(err).ToNot(HaveOccurred(), pods)
				g.Expect(pods).To(ContainSubstring("Running"), "replacement pod should be running")
			}).Should(Succeed())

			waitForLeadership(k, "fleet-agent", pod)
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
			pod = waitForPodReadyAndTerminate(k, labels)
		})

		It("exits gracefully", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get(
					"pod",
					pod,
					"-o", `jsonpath={.status.containerStatuses[?(@.name=="fleet-controller")].state.terminated}`,
				)
				if err != nil {
					g.Expect(strings.Contains(strings.ToLower(out), "notfound")).To(BeTrue(), out)
					return
				}

				state, err := parseTerminatedState(out)
				g.Expect(err).ToNot(HaveOccurred(), out)
				g.Expect(state).NotTo(BeNil(), "pod should have terminated")

				g.Expect(state.ExitCode).To(Equal(0))
				g.Expect(state.Reason).To(Equal("Completed"))
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				pods, err := k.Get("pod", "-l", labels, "-o", "jsonpath={.items[*].status.phase}")
				g.Expect(err).ToNot(HaveOccurred(), pods)
				g.Expect(pods).To(ContainSubstring("Running"), "replacement pod should be running")
			}).Should(Succeed())

			waitForLeadership(k, "fleet-controller-leader-election-shard", pod)
		})
	})

	When("the gitjob deployment receives SIGTERM", func() {
		BeforeEach(func() {
			ns = "cattle-fleet-system"
			k = env.Kubectl.Namespace(ns)
			labels = "app=gitjob,fleet.cattle.io/shard-default=true"
		})

		JustBeforeEach(func() {
			pod = waitForPodReadyAndTerminate(k, labels)
		})

		It("exits gracefully", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get(
					"pod",
					pod,
					"-o", "jsonpath={.status.containerStatuses[0].state.terminated}",
				)
				if err != nil {
					g.Expect(strings.Contains(strings.ToLower(out), "notfound")).To(BeTrue(), out)
					return
				}

				state, err := parseTerminatedState(out)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(state).NotTo(BeNil(), "pod should have terminated")

				g.Expect(state.ExitCode).To(Equal(0))
				g.Expect(state.Reason).To(Equal("Completed"))
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				pods, err := k.Get("pod", "-l", labels, "-o", "jsonpath={.items[*].status.phase}")
				g.Expect(err).ToNot(HaveOccurred(), pods)
				g.Expect(pods).To(ContainSubstring("Running"), "replacement pod should be running")
			}).Should(Succeed())

			waitForLeadership(k, "fleet-gitops-leader-election-shard", pod)
		})
	})

	When("the helmops deployment receives SIGTERM", func() {
		BeforeEach(func() {
			ns = "cattle-fleet-system"
			k = env.Kubectl.Namespace(ns)
			labels = "app=helmops,fleet.cattle.io/shard-default=true"
		})

		JustBeforeEach(func() {
			pod = waitForPodReadyAndTerminate(k, labels)
		})

		It("exits gracefully", func() {
			Eventually(func(g Gomega) {
				out, err := k.Get(
					"pod",
					pod,
					"-o", "jsonpath={.status.containerStatuses[0].state.terminated}",
				)
				if err != nil {
					g.Expect(strings.Contains(strings.ToLower(out), "notfound")).To(BeTrue(), out)
					return
				}

				state, err := parseTerminatedState(out)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(state).NotTo(BeNil(), "pod should have terminated")

				g.Expect(state.ExitCode).To(Equal(0))
				g.Expect(state.Reason).To(Equal("Completed"))
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				pods, err := k.Get("pod", "-l", labels, "-o", "jsonpath={.items[*].status.phase}")
				g.Expect(err).ToNot(HaveOccurred(), pods)
				g.Expect(pods).To(ContainSubstring("Running"), "replacement pod should be running")
			}).Should(Succeed())

			waitForLeadership(k, "fleet-helmops-leader-election-shard", pod)
		})
	})
})
