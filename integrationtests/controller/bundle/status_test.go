package bundle

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Bundle Status Fields", func() {

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

	When("BundleDeployment changes", func() {
		BeforeEach(func() {
			cluster, err := utils.CreateCluster(ctx, k8sClient, "cluster", namespace, nil, namespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster).To(Not(BeNil()))
			targets := []v1alpha1.BundleTarget{
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						TargetNamespace: "targetNs",
					},
					Name:        "cluster",
					ClusterName: "cluster",
				},
			}
			bundle, err := utils.CreateBundle(ctx, k8sClient, "name", namespace, targets, targets)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle).To(Not(BeNil()))
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(ctx, &v1alpha1.Bundle{ObjectMeta: metav1.ObjectMeta{
				Name:      "name",
				Namespace: namespace,
			}})).NotTo(HaveOccurred())

		})

		It("updates the status fields", func() {
			bundle := &v1alpha1.Bundle{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bundle)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle).To(Not(BeNil()))
			Expect(bundle.Status.Summary.Ready).To(Equal(0))
			Expect(bundle.Status.Summary.DesiredReady).To(Equal(0))
			Expect(bundle.Status.Display.ReadyClusters).To(Equal(""))

			By("To reflect the 'Ready' status in bundle, bundleDeployment needs below mentioned status fields.")
			bd := &v1alpha1.BundleDeployment{}
			Eventually(func() error {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bd)
				if err != nil {
					return err
				}
				bd.Status.Display.State = "Ready"
				bd.Status.AppliedDeploymentID = bd.Spec.DeploymentID
				bd.Status.Ready = true
				bd.Status.NonModified = true
				return k8sClient.Status().Update(ctx, bd)
			}).ShouldNot(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bd)
			Expect(err).NotTo(HaveOccurred())
			Expect(bd.Status.Display.State).To(Equal("Ready"))
			Expect(bd.Status.AppliedDeploymentID).To(Equal(bd.Spec.DeploymentID))

			Eventually(func() bool {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bundle)
				Expect(err).NotTo(HaveOccurred())
				return bundle.Status.Summary.Ready == 1
			}).Should(BeTrue())
			Expect(bundle.Status.Summary.DesiredReady).To(Equal(1))
			Expect(bundle.Status.Display.ReadyClusters).To(Equal("1/1"))
		})
	})

	When("Cluster changes", func() {
		BeforeEach(func() {
			cluster, err := utils.CreateCluster(ctx, k8sClient, "cluster", namespace, nil, namespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster).To(Not(BeNil()))
			targets := []v1alpha1.BundleTarget{
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						TargetNamespace: "targetNs",
					},
					Name:        "cluster",
					ClusterName: "cluster",
				},
			}
			bundle, err := utils.CreateBundle(ctx, k8sClient, "name", namespace, targets, targets)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle).To(Not(BeNil()))
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(ctx, &v1alpha1.Bundle{ObjectMeta: metav1.ObjectMeta{
				Name:      "name",
				Namespace: namespace,
			}})).NotTo(HaveOccurred())

		})

		It("updates the status fields", func() {
			cluster := &v1alpha1.Cluster{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "cluster"}, cluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster.Status.Summary.Ready).To(Equal(0))
			Expect(cluster.Status.Display.ReadyBundles).To(Equal("0/0"))

			bundle := &v1alpha1.Bundle{}
			Eventually(func() bool {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bundle)
				Expect(err).NotTo(HaveOccurred())
				return bundle.Status.Summary.Ready == 0
			}).Should(BeTrue())

			// prepare bundle deployment so it satisfies the status change
			bd := &v1alpha1.BundleDeployment{}
			Eventually(func() error {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bd)
				if err != nil {
					return err
				}
				bd.Status.Display.State = "Ready"
				bd.Status.AppliedDeploymentID = bd.Spec.DeploymentID
				bd.Status.Ready = true
				bd.Status.NonModified = true
				return k8sClient.Status().Update(ctx, bd)
			}).ShouldNot(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bd)
			Expect(err).NotTo(HaveOccurred())
			Expect(bd.Status.Display.State).To(Equal("Ready"))
			Expect(bd.Status.AppliedDeploymentID).To(Equal(bd.Spec.DeploymentID))

			Eventually(func() bool {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bundle)
				Expect(err).NotTo(HaveOccurred())
				return bundle.Status.Summary.Ready == 1
			}).Should(BeTrue())

			Eventually(func() bool {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "cluster"}, cluster)
				Expect(err).NotTo(HaveOccurred())
				return cluster.Status.Summary.Ready == 1
			}).Should(BeTrue())
			Expect(cluster.Status.Summary.Pending).To(Equal(0))
			Expect(cluster.Status.Display.ReadyBundles).To(Equal("1/2"))

			// resourceVersion := cluster.ResourceVersion
			By("Modifying labels will change cluster state")
			modifiedLabels := map[string]string{"foo": "bar"}
			Eventually(func() error {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "cluster"}, cluster)
				Expect(err).NotTo(HaveOccurred())
				cluster.Labels = modifiedLabels
				return k8sClient.Update(ctx, cluster)
			}).ShouldNot(HaveOccurred())

			// Change in cluster state reflects a change in bundle state
			Eventually(func() bool {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bundle)
				Expect(err).NotTo(HaveOccurred())
				return bundle.Status.Summary.Ready == 0
			}).Should(BeTrue())
			Expect(bundle.Status.Summary.WaitApplied).To(Equal(1))
			Expect(bundle.Status.Display.ReadyClusters).To(Equal("0/1"))
			Expect(bundle.Status.Display.State).To(Equal("WaitApplied"))
		})
	})
})
