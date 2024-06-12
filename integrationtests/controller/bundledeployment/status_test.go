package bundledeployment

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("BundleDeployment Status Fields", func() {

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
			options := v1alpha1.BundleDeploymentOptions{
				TargetNamespace: "targetNs",
			}
			bd, err := createBundleDeployment("name", namespace, options)
			Expect(err).NotTo(HaveOccurred())
			Expect(bd).To(Not(BeNil()))
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(ctx, &v1alpha1.BundleDeployment{ObjectMeta: metav1.ObjectMeta{
				Name:      "name",
				Namespace: namespace,
			}})).NotTo(HaveOccurred())

		})

		It("updates the status fields", func() {
			bd := &v1alpha1.BundleDeployment{}
			Eventually(func() error {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bd)
				if err != nil {
					return err
				}
				bd.Status.AppliedDeploymentID = bd.Spec.DeploymentID
				bd.Status.Ready = true
				bd.Status.NonModified = true
				return k8sClient.Status().Update(ctx, bd)
			}).ShouldNot(HaveOccurred())

			Eventually(func() error {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bd)
				Expect(err).ToNot(HaveOccurred())
				bd.Spec.StagedDeploymentID = bd.Spec.DeploymentID
				err = k8sClient.Update(ctx, bd)
				if err != nil {
					return err
				}
				if bd.Status.Display.State != "Ready" {
					return errors.New("bundle deployment not ready")
				}
				return nil
			}).ShouldNot(HaveOccurred())
			Expect(bd.Status.Display.State).To(Equal("Ready"))
			Expect(bd.Spec.StagedDeploymentID).To(Equal(bd.Spec.DeploymentID))

			By("Updating the bundledeployment spec, updates status fields")
			Expect(bd.Spec.Options.DeleteNamespace).ToNot(BeTrue())

			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bd)
			Expect(err).NotTo(HaveOccurred())
			bd.Status.NonModified = false
			err = k8sClient.Status().Update(ctx, bd)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bd)
			Expect(bd.Status.NonModified).To(BeFalse())
			Expect(bd.Status.Ready).To(BeTrue())
			Expect(bd.Status.Display.State).To(Equal("Ready"))

			Eventually(func() error {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bd)
				Expect(err).NotTo(HaveOccurred())
				bd.Spec.Options.DeleteNamespace = true
				return k8sClient.Update(ctx, bd)
			}).ShouldNot(HaveOccurred())

			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bd)
			Expect(err).NotTo(HaveOccurred())
			Expect(bd.Status.Display.State).To(Equal("Modified"))
		})
	})
})
