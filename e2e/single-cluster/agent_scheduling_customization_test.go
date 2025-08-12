package singlecluster_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func ptr[T any](v T) *T {
	return &v
}

var _ = Describe("Agent Scheduling Customization", func() {
	var (
		cluster *fleet.Cluster
	)

	BeforeEach(func() {
		var err error
		cluster = &fleet.Cluster{}
		err = clientUpstream.Get(context.TODO(), client.ObjectKey{
			Namespace: env.Namespace,
			Name:      "local",
		}, cluster)
		Expect(err).ToNot(HaveOccurred())
	})

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

		AfterEach(func() {
			// Clean up by removing the agentSchedulingCustomization
			Eventually(func(g Gomega) {
				// Re-fetch the cluster to get the latest version
				latestCluster := &fleet.Cluster{}
				err := clientUpstream.Get(context.TODO(), client.ObjectKey{
					Namespace: env.Namespace,
					Name:      "local",
				}, latestCluster)
				g.Expect(err).ToNot(HaveOccurred())

				latestCluster.Spec.AgentSchedulingCustomization = nil
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
				g.Expect(cluster.Status.AgentSchedulingCustomizationHash).To(Equal(""))
			}).Should(Succeed(), "Should have cleared the AgentSchedulingCustomizationHash")

			// Wait for PriorityClass to be deleted
			Eventually(func() bool {
				pc := &schedulingv1.PriorityClass{}
				err := clientUpstream.Get(context.TODO(), client.ObjectKey{
					Name: "fleet-agent-priority-class",
				}, pc)
				return errors.IsNotFound(err)
			}).Should(BeTrue(), "PriorityClass should be deleted")
		})

		It("should create a PriorityClass on the cluster", func() {
			By("waiting for the PriorityClass to be created")
			Eventually(func(g Gomega) {
				pc := &schedulingv1.PriorityClass{}
				err := clientUpstream.Get(context.TODO(), client.ObjectKey{
					Name: "fleet-agent-priority-class",
				}, pc)
				g.Expect(err).ToNot(HaveOccurred(), "PriorityClass should be created")

				g.Expect(pc.Value).To(Equal(int32(1000)), "PriorityClass should have correct value")
				g.Expect(pc.PreemptionPolicy).ToNot(BeNil())
				g.Expect(*pc.PreemptionPolicy).To(Equal(corev1.PreemptNever), "PriorityClass should have correct preemption policy")
				g.Expect(pc.Description).To(Equal("Priority class for Fleet Agent"))
			}).Should(Succeed())

			By("checking that the agent deployment uses the priority class")
			k := env.Kubectl.Namespace("cattle-fleet-local-system")
			Eventually(func(g Gomega) {
				out, err := k.Get("deployment", "fleet-agent", "-o", "jsonpath={.spec.template.spec.priorityClassName}")
				g.Expect(err).ToNot(HaveOccurred())
				priorityClassName := strings.TrimSpace(out)
				g.Expect(priorityClassName).To(Equal("fleet-agent-priority-class"))
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

		AfterEach(func() {
			// Clean up by removing the agentSchedulingCustomization
			Eventually(func(g Gomega) {
				// Re-fetch the cluster to get the latest version
				latestCluster := &fleet.Cluster{}
				err := clientUpstream.Get(context.TODO(), client.ObjectKey{
					Namespace: env.Namespace,
					Name:      "local",
				}, latestCluster)
				g.Expect(err).ToNot(HaveOccurred())

				latestCluster.Spec.AgentSchedulingCustomization = nil
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
				g.Expect(cluster.Status.AgentSchedulingCustomizationHash).To(Equal(""))
			}).Should(Succeed(), "Should have cleared the AgentSchedulingCustomizationHash")
		})

		It("should create a PodDisruptionBudget on the cluster", func() {
			By("waiting for the PodDisruptionBudget to be created")
			k := env.Kubectl.Namespace("cattle-fleet-local-system")
			Eventually(func(g Gomega) {
				out, err := k.Get("pdb", "fleet-agent-pod-disruption-budget", "-o", "jsonpath={.spec.maxUnavailable}")
				g.Expect(err).ToNot(HaveOccurred(), "PodDisruptionBudget should be created")
				maxUnavailable := strings.TrimSpace(out)
				g.Expect(maxUnavailable).To(Equal("1"), "PodDisruptionBudget should have correct maxUnavailable")
			}).Should(Succeed())

			By("checking that the PDB has correct selector")
			Eventually(func(g Gomega) {
				out, err := k.Get("pdb", "fleet-agent-pod-disruption-budget", "-o", "jsonpath={.spec.selector.matchLabels.app}")
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

		AfterEach(func() {
			// Clean up by removing the agentSchedulingCustomization
			Eventually(func(g Gomega) {
				// Re-fetch the cluster to get the latest version
				latestCluster := &fleet.Cluster{}
				err := clientUpstream.Get(context.TODO(), client.ObjectKey{
					Namespace: env.Namespace,
					Name:      "local",
				}, latestCluster)
				g.Expect(err).ToNot(HaveOccurred())

				latestCluster.Spec.AgentSchedulingCustomization = nil
				err = clientUpstream.Update(context.TODO(), latestCluster)
				g.Expect(err).ToNot(HaveOccurred())
			}).Should(Succeed(), "Should be able to update cluster to remove agentSchedulingCustomization")
		})

		It("should create both PriorityClass and PodDisruptionBudget", func() {
			By("checking PriorityClass is created")
			Eventually(func(g Gomega) {
				pc := &schedulingv1.PriorityClass{}
				err := clientUpstream.Get(context.TODO(), client.ObjectKey{
					Name: "fleet-agent-priority-class",
				}, pc)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(pc.Value).To(Equal(int32(500)))
			}).Should(Succeed())

			By("checking PodDisruptionBudget is created")
			k := env.Kubectl.Namespace("cattle-fleet-local-system")
			Eventually(func(g Gomega) {
				out, err := k.Get("pdb", "fleet-agent-pod-disruption-budget", "-o", "jsonpath={.spec.minAvailable}")
				g.Expect(err).ToNot(HaveOccurred())
				minAvailable := strings.TrimSpace(out)
				g.Expect(minAvailable).To(Equal("1"))
			}).Should(Succeed())
		})
	})
})
