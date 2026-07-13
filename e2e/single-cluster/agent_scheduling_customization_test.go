package singlecluster_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	localAgentNamespace   = "cattle-fleet-local-system"
	agentPriorityClass    = "fleet-agent-priority-class"
	agentDisruptionBudget = "fleet-agent-pod-disruption-budget"
)

func ptr[T any](v T) *T {
	return new(v)
}

// removeAgentSchedulingCustomization clears the customization from the local cluster and
// waits until the controller has fully reconciled the removal: the status hash is empty
// and both resources the customization owns are gone. Every spec here shares the same
// local cluster, so returning before the cleanup lands would leak a PriorityClass or a
// PDB into whichever spec runs next and make the suite order-dependent.
func removeAgentSchedulingCustomization() {
	Eventually(func(g Gomega) {
		latestCluster := &fleet.Cluster{}
		err := clientUpstream.Get(context.TODO(), client.ObjectKey{
			Namespace: env.Namespace,
			Name:      "local",
		}, latestCluster)
		g.Expect(err).ToNot(HaveOccurred())

		latestCluster.Spec.AgentSchedulingCustomization = nil
		// Bump RedeployAgentGeneration to force a prompt agent re-import.
		// Removing the customization clears the status hash quickly, but the
		// re-import that prunes the PriorityClass and the PDB is otherwise only
		// triggered on the controller's periodic resync (~5m). That cadence races
		// the deletion waits below and makes the specs flaky, so we force the
		// redeploy here to prune both immediately.
		latestCluster.Spec.RedeployAgentGeneration++
		err = clientUpstream.Update(context.TODO(), latestCluster)
		g.Expect(err).ToNot(HaveOccurred())
	}).Should(Succeed(), "Should be able to update cluster to remove agentSchedulingCustomization")

	Eventually(func(g Gomega) {
		latestCluster := &fleet.Cluster{}
		err := clientUpstream.Get(context.TODO(), client.ObjectKey{
			Namespace: env.Namespace,
			Name:      "local",
		}, latestCluster)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(latestCluster.Status.AgentSchedulingCustomizationHash).To(Equal(""))
	}).Should(Succeed(), "Should have cleared the AgentSchedulingCustomizationHash")

	Eventually(func(g Gomega) {
		pc := &schedulingv1.PriorityClass{}
		err := clientUpstream.Get(context.TODO(), client.ObjectKey{
			Name: agentPriorityClass,
		}, pc)
		g.Expect(errors.IsNotFound(err)).To(BeTrue())
	}).Should(Succeed(), "PriorityClass should be deleted")

	Eventually(func(g Gomega) {
		pdb := &policyv1.PodDisruptionBudget{}
		err := clientUpstream.Get(context.TODO(), client.ObjectKey{
			Namespace: localAgentNamespace,
			Name:      agentDisruptionBudget,
		}, pdb)
		g.Expect(errors.IsNotFound(err)).To(BeTrue())
	}).Should(Succeed(), "PodDisruptionBudget should be deleted")
}

var _ = Describe("Agent Scheduling Customization", func() {
	When("agentSchedulingCustomization.PriorityClass is configured on the cluster resource", func() {
		BeforeEach(func() {
			// Update the cluster with agentSchedulingCustomization
			Eventually(func(g Gomega) {
				// Re-fetch the cluster to get the latest version
				latestCluster := &fleet.Cluster{}
				err := clientUpstream.Get(context.TODO(), client.ObjectKey{
					Namespace: env.Namespace,
					Name:      "local",
				}, latestCluster)
				g.Expect(err).ToNot(HaveOccurred())

				latestCluster.Spec.AgentSchedulingCustomization = &fleet.AgentSchedulingCustomization{
					PriorityClass: &fleet.PriorityClassSpec{
						Value:            1000,
						PreemptionPolicy: ptr(corev1.PreemptNever),
					},
				}

				err = clientUpstream.Update(context.TODO(), latestCluster)
				g.Expect(err).ToNot(HaveOccurred())
			}).Should(Succeed(), "Should be able to update cluster with agentSchedulingCustomization")
		})

		AfterEach(removeAgentSchedulingCustomization)

		It("should create a PriorityClass on the cluster", func() {
			By("waiting for the PriorityClass to be created")
			Eventually(func(g Gomega) {
				pc := &schedulingv1.PriorityClass{}
				err := clientUpstream.Get(context.TODO(), client.ObjectKey{
					Name: agentPriorityClass,
				}, pc)
				g.Expect(err).ToNot(HaveOccurred(), "PriorityClass should be created")

				g.Expect(pc.Value).To(Equal(int32(1000)), "PriorityClass should have correct value")
				g.Expect(pc.PreemptionPolicy).ToNot(BeNil())
				g.Expect(*pc.PreemptionPolicy).To(Equal(corev1.PreemptNever), "PriorityClass should have correct preemption policy")
				g.Expect(pc.Description).To(Equal("Priority class for Fleet Agent"))
			}).Should(Succeed())

			By("checking that the agent deployment uses the priority class")
			k := env.Kubectl.Namespace(localAgentNamespace)
			Eventually(func(g Gomega) {
				out, err := k.Get("deployment", "fleet-agent", "-o", "jsonpath={.spec.template.spec.priorityClassName}")
				g.Expect(err).ToNot(HaveOccurred())
				priorityClassName := strings.TrimSpace(out)
				g.Expect(priorityClassName).To(Equal(agentPriorityClass))
			}).Should(Succeed())
		})

		It("should update cluster status hash when agentSchedulingCustomization changes", func() {
			By("checking that agentSchedulingCustomizationHash is set in cluster status")
			Eventually(func(g Gomega) {
				updatedCluster := &fleet.Cluster{}
				err := clientUpstream.Get(context.TODO(), client.ObjectKey{
					Namespace: env.Namespace,
					Name:      "local",
				}, updatedCluster)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(updatedCluster.Status.AgentSchedulingCustomizationHash).ToNot(BeEmpty())
			}).Should(Succeed())
		})
	})

	When("agentSchedulingCustomization.PodDisruptionBudget is configured on local cluster", func() {
		BeforeEach(func() {
			// Update the cluster with agentSchedulingCustomization for PDB
			Eventually(func(g Gomega) {
				// Re-fetch the cluster to get the latest version
				latestCluster := &fleet.Cluster{}
				err := clientUpstream.Get(context.TODO(), client.ObjectKey{
					Namespace: env.Namespace,
					Name:      "local",
				}, latestCluster)
				g.Expect(err).ToNot(HaveOccurred())

				latestCluster.Spec.AgentSchedulingCustomization = &fleet.AgentSchedulingCustomization{
					PodDisruptionBudget: &fleet.PodDisruptionBudgetSpec{
						MaxUnavailable: "1",
					},
				}

				err = clientUpstream.Update(context.TODO(), latestCluster)
				g.Expect(err).ToNot(HaveOccurred())
			}).Should(Succeed(), "Should be able to update cluster with agentSchedulingCustomization")
		})

		AfterEach(removeAgentSchedulingCustomization)

		It("should create a PodDisruptionBudget on the cluster", func() {
			By("waiting for the PodDisruptionBudget to be created")
			k := env.Kubectl.Namespace(localAgentNamespace)
			Eventually(func(g Gomega) {
				out, err := k.Get("pdb", agentDisruptionBudget, "-o", "jsonpath={.spec.maxUnavailable}")
				g.Expect(err).ToNot(HaveOccurred(), "PodDisruptionBudget should be created")
				maxUnavailable := strings.TrimSpace(out)
				g.Expect(maxUnavailable).To(Equal("1"), "PodDisruptionBudget should have correct maxUnavailable")
			}).Should(Succeed())

			By("checking that the PDB has correct selector")
			Eventually(func(g Gomega) {
				out, err := k.Get("pdb", agentDisruptionBudget, "-o", "jsonpath={.spec.selector.matchLabels.app}")
				g.Expect(err).ToNot(HaveOccurred())
				app := strings.TrimSpace(out)
				g.Expect(app).To(Equal("fleet-agent"))
			}).Should(Succeed())
		})
	})

	When("both PriorityClass and PodDisruptionBudget are configured", func() {
		BeforeEach(func() {
			// Update the cluster with both configurations
			Eventually(func(g Gomega) {
				// Re-fetch the cluster to get the latest version
				latestCluster := &fleet.Cluster{}
				err := clientUpstream.Get(context.TODO(), client.ObjectKey{
					Namespace: env.Namespace,
					Name:      "local",
				}, latestCluster)
				g.Expect(err).ToNot(HaveOccurred())

				latestCluster.Spec.AgentSchedulingCustomization = &fleet.AgentSchedulingCustomization{
					PriorityClass: &fleet.PriorityClassSpec{
						Value: 500,
					},
					PodDisruptionBudget: &fleet.PodDisruptionBudgetSpec{
						MinAvailable: "1",
					},
				}

				err = clientUpstream.Update(context.TODO(), latestCluster)
				g.Expect(err).ToNot(HaveOccurred())
			}).Should(Succeed(), "Should be able to update cluster with agentSchedulingCustomization")
		})

		AfterEach(removeAgentSchedulingCustomization)

		It("should create both PriorityClass and PodDisruptionBudget", func() {
			By("checking PriorityClass is created")
			Eventually(func(g Gomega) {
				pc := &schedulingv1.PriorityClass{}
				err := clientUpstream.Get(context.TODO(), client.ObjectKey{
					Name: agentPriorityClass,
				}, pc)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(pc.Value).To(Equal(int32(500)))
			}).Should(Succeed())

			By("checking PodDisruptionBudget is created")
			k := env.Kubectl.Namespace(localAgentNamespace)
			Eventually(func(g Gomega) {
				out, err := k.Get("pdb", agentDisruptionBudget, "-o", "jsonpath={.spec.minAvailable}")
				g.Expect(err).ToNot(HaveOccurred())
				minAvailable := strings.TrimSpace(out)
				g.Expect(minAvailable).To(Equal("1"))
			}).Should(Succeed())
		})
	})
})
