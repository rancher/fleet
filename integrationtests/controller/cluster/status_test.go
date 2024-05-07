package cluster

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/cmd/controller/summary"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v2/pkg/condition"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Cluster Status Fields", func() {

	var (
		gitrepo *fleet.GitRepo
		bd      *fleet.BundleDeployment
	)
	const deploymentID = "test123"

	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).ToNot(HaveOccurred())

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, ns)).ToNot(HaveOccurred())
		})
	})

	When("Bundledeployment is added", func() {
		BeforeEach(func() {
			cluster := &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: namespace,
				},
				Spec: fleet.ClusterSpec{},
			}

			err := k8sClient.Create(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			// simulate agentmanagement updating the status to set the namespace
			cluster.Status.Agent.LastSeen = metav1.Now()
			cluster.Status.Namespace = namespace
			err = k8sClient.Status().Update(ctx, cluster)
			Expect(err).NotTo(HaveOccurred())

			gitrepo = &fleet.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-gitrepo",
					Namespace: namespace,
				},
				Spec: fleet.GitRepoSpec{},
			}
			err = k8sClient.Create(ctx, gitrepo)
			Expect(err).NotTo(HaveOccurred())

			bd = &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-bd",
					Namespace: namespace,
					Labels: map[string]string{
						"fleet.cattle.io/repo-name":        "test-gitrepo",
						"fleet.cattle.io/bundle-namespace": namespace,
					},
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID:       deploymentID,
					StagedDeploymentID: deploymentID,
				},
			}
			err = k8sClient.Create(ctx, bd)
			Expect(err).NotTo(HaveOccurred())

		})

		It("updates the status fields", func() {
			cluster := &fleet.Cluster{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "test-cluster", Namespace: namespace}, cluster)
				Expect(err).NotTo(HaveOccurred())

				return cluster.Status.Summary.DesiredReady == 1
			}).Should(BeTrue())

			fmt.Printf("### cluster: %#v\n", cluster.Status)
			Expect(cluster.Status.Summary.Ready).To(Equal(0))
			Expect(cluster.Status.Summary.NonReadyResources).To(HaveLen(1))

			Expect(cluster.Status.Display.ReadyBundles).To(Equal("0/1"))
			Expect(cluster.Status.Display.State).To(Equal("WaitApplied"))

			Expect(cluster.Status.DesiredReadyGitRepos).To(Equal(1))
			Expect(cluster.Status.ReadyGitRepos).To(Equal(0))

			Expect(condition.Cond(fleet.ClusterConditionReady).IsFalse(cluster)).To(BeTrue())
			// Was `NotReady(1) [Bundle test-bd]`
			Expect(condition.Cond(fleet.ClusterConditionReady).GetMessage(cluster)).To(Equal("WaitApplied(1) [Bundle test-bd]"))
			Expect(condition.Cond(fleet.ClusterConditionProcessed).IsTrue(cluster)).To(BeTrue())

			By("updating the bundledeployment to be ready")
			bd.Status.AppliedDeploymentID = deploymentID
			bd.Status.Ready = true
			bd.Status.NonModified = true
			err := k8sClient.Status().Update(ctx, bd)
			Expect(err).NotTo(HaveOccurred())
			fmt.Printf("### bd: %#v, %v\n", bd.Status, summary.GetDeploymentState(bd))

			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "test-cluster", Namespace: namespace}, cluster)
				Expect(err).NotTo(HaveOccurred())

				fmt.Printf("### cluster: %#v\n", cluster.Status)

				return cluster.Status.Summary.Ready == 1
			}).Should(BeTrue())

			// Expect(cluster.Status.DesiredReadyGitRepos).To(Equal(0))
			// Expect(cluster.Status.ReadyGitRepos).To(Equal(0))

			// Expect(cluster.Status.Summary.ErrApplied).To(Equal(0))

			// Expect(cluster.Status.ResourceCounts).To(HaveLen(1))
			// Expect(cluster.Status.ResourceCounts).To(ContainElement(fleet.GitRepoResourceCounts{}))

			// Expect(cluster.Status.Display.ReadyBundles).To(Equal("1/1"))
			// Expect(cluster.Status.Display.State).To(Equal(fleet.Ready))
			By("deleting the bundledeployment")
		})
	})
})
