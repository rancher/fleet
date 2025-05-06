package controller

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/rancher/fleet/integrationtests/utils"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("HelmOp Status Fields", func() {
	var (
		helmop *fleet.HelmOp
		bd     *fleet.BundleDeployment
	)

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

	When("Bundle changes", func() {
		BeforeEach(func() {
			os.Setenv("EXPERIMENTAL_HELM_OPS", "true")
			cluster, err := utils.CreateCluster(ctx, k8sClient, "cluster", namespace, nil, namespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster).To(Not(BeNil()))
			targets := []fleet.BundleTarget{
				{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						TargetNamespace: "targetNs",
					},
					Name:        "cluster",
					ClusterName: "cluster",
				},
			}
			bundle, err := utils.CreateBundle(ctx, k8sClient, "name", namespace, targets, targets)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle).To(Not(BeNil()))

			helmop = &fleet.HelmOp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-helmop",
					Namespace: namespace,
				},
				Spec: fleet.HelmOpSpec{
					BundleSpec: fleet.BundleSpec{
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Chart: "test",
							},
						},
					},
				},
			}
			err = k8sClient.Create(ctx, helmop)
			Expect(err).NotTo(HaveOccurred())

			bd = &fleet.BundleDeployment{}
			Eventually(func(g Gomega) {
				nsName := types.NamespacedName{Namespace: namespace, Name: "name"}
				g.Expect(k8sClient.Get(ctx, nsName, bd)).ToNot(HaveOccurred())
			}).Should(Succeed())
		})

		It("updates the status fields", func() {
			bundle := &fleet.Bundle{}
			bundleName := types.NamespacedName{Namespace: namespace, Name: "name"}
			helmOpName := types.NamespacedName{Namespace: namespace, Name: helmop.Name}
			By("Receiving a bundle update")
			Eventually(func() error {
				err := k8sClient.Get(ctx, bundleName, bundle)
				Expect(err).ToNot(HaveOccurred())
				bundle.Labels[fleet.HelmOpLabel] = helmop.Name
				return k8sClient.Update(ctx, bundle)
			}).ShouldNot(HaveOccurred())
			Expect(bundle.Status.Summary.Ready).ToNot(Equal(1))

			err := k8sClient.Get(ctx, helmOpName, helmop)
			Expect(err).ToNot(HaveOccurred())
			Expect(helmop.Status.Summary.Ready).To(Equal(0))
			Expect(helmop.Status.ReadyClusters).To(Equal(0))

			// This simulates what the bundle deployment reconciler would do.
			By("Updating the BundleDeployment status to ready")
			bd := &fleet.BundleDeployment{}
			Eventually(func() error {
				err := k8sClient.Get(ctx, bundleName, bd)
				if err != nil {
					return err
				}
				bd.Status.Display.State = "Ready"
				bd.Status.AppliedDeploymentID = bd.Spec.DeploymentID
				bd.Status.Ready = true
				bd.Status.NonModified = true
				return k8sClient.Status().Update(ctx, bd)
			}).ShouldNot(HaveOccurred())

			// waiting for the bundle to update
			Eventually(func() bool {
				err := k8sClient.Get(ctx, bundleName, bundle)
				Expect(err).NotTo(HaveOccurred())
				return bundle.Status.Summary.Ready == 1
			}).Should(BeTrue())

			// waiting for the HelmOp to update
			Eventually(func(g Gomega) {
				err = k8sClient.Get(ctx, helmOpName, helmop)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(helmop.Status.Summary.Ready).To(Equal(1))
				g.Expect(helmop.Status.ReadyClusters).To(Equal(1))
				g.Expect(helmop.Status.DesiredReadyClusters).To(Equal(1))
			}).Should(Succeed())

			By("Deleting a bundle")
			err = k8sClient.Delete(ctx, bundle)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, helmOpName, helmop)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(helmop.Status.Summary.Ready).To(Equal(0))
				g.Expect(helmop.Status.Summary.DesiredReady).To(Equal(0))
				g.Expect(helmop.Status.Display.ReadyBundleDeployments).To(Equal("0/0"))
			}).Should(Succeed())
		})
	})
})
