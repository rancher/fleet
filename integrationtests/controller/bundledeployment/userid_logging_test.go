package bundledeployment

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("UserID logging", func() {
	BeforeEach(func() {
		logsBuffer.Reset()
	})

	When("BundleDeployment has userID label", func() {
		const userID = "test-user-123"

		var (
			bd        *fleet.BundleDeployment
			namespace string
		)

		BeforeEach(func() {
			namespace = "default"

			bd = &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-bd-",
					Namespace:    namespace,
					Labels: map[string]string{
						fleet.CreatedByUserIDLabel: userID,
					},
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "test-deployment",
					Options: fleet.BundleDeploymentOptions{
						DefaultNamespace: "default",
					},
				},
			}

			err := k8sClient.Create(context.Background(), bd)
			Expect(err).ToNot(HaveOccurred())

			// Update status to trigger reconciliation (BundleDeployment controller only reconciles on status changes)
			Eventually(func() error {
				err := k8sClient.Get(context.Background(), types.NamespacedName{Name: bd.Name, Namespace: bd.Namespace}, bd)
				if err != nil {
					return err
				}
				bd.Status.Display.State = "TestState"
				return k8sClient.Status().Update(context.Background(), bd)
			}).Should(Succeed())
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(context.Background(), bd)).ToNot(HaveOccurred())
		})

		It("logs userID in reconciliation", func() {
			Eventually(logsBuffer.String).Should(ContainSubstring(bd.Name))

			logs := logsBuffer.String()
			bdLogs := utils.ExtractResourceLogs(logs, bd.Name)
			Expect(bdLogs).To(Or(
				ContainSubstring(`"userID":"`+userID+`"`),
				ContainSubstring(`"userID": "`+userID+`"`),
			))
		})
	})

	When("BundleDeployment does not have userID label", func() {
		var (
			bd        *fleet.BundleDeployment
			namespace string
		)

		BeforeEach(func() {
			namespace = "default"

			bd = &fleet.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-bd-no-user-",
					Namespace:    namespace,
				},
				Spec: fleet.BundleDeploymentSpec{
					DeploymentID: "test-deployment",
					Options: fleet.BundleDeploymentOptions{
						DefaultNamespace: "default",
					},
				},
			}

			err := k8sClient.Create(context.Background(), bd)
			Expect(err).ToNot(HaveOccurred())

			// Update status to trigger reconciliation
			Eventually(func() error {
				err := k8sClient.Get(context.Background(), types.NamespacedName{Name: bd.Name, Namespace: bd.Namespace}, bd)
				if err != nil {
					return err
				}
				bd.Status.Display.State = "TestState"
				return k8sClient.Status().Update(context.Background(), bd)
			}).Should(Succeed())
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(context.Background(), bd)).ToNot(HaveOccurred())
		})

		It("does not log userID in reconciliation", func() {
			Eventually(logsBuffer.String).Should(ContainSubstring(bd.Name))

			logs := logsBuffer.String()
			bdLogs := utils.ExtractResourceLogs(logs, bd.Name)
			Expect(bdLogs).NotTo(ContainSubstring(`"userID"`))
		})
	})
})
