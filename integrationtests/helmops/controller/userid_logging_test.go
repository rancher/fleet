package controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("HelmOp UserID logging", func() {
	var (
		helmop    *v1alpha1.HelmOp
		namespace string
	)

	createHelmOp := func(name string, labels map[string]string) {
		helmop = &v1alpha1.HelmOp{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
			},
			Spec: v1alpha1.HelmOpSpec{
				BundleSpec: v1alpha1.BundleSpec{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						DefaultNamespace: "default",
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, helmop)).ToNot(HaveOccurred())
	}

	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).ToNot(HaveOccurred())

		logsBuffer.Reset()

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, helmop)).ToNot(HaveOccurred())
			Expect(k8sClient.Delete(ctx, ns)).ToNot(HaveOccurred())
		})
	})

	When("HelmOp has user ID label", func() {
		const userID = "user-12345"

		BeforeEach(func() {
			createHelmOp("test-helmop-with-userid", map[string]string{
				v1alpha1.CreatedByUserIDLabel: userID,
			})
		})

		It("includes userID in log output", func() {
			Eventually(logsBuffer.String, timeout).Should(Or(
				ContainSubstring(`"userID":"`+userID+`"`),
				ContainSubstring(`"userID": "`+userID+`"`),
			))

			logs := logsBuffer.String()
			Expect(logs).To(ContainSubstring("HelmOp"))
			Expect(logs).To(ContainSubstring(helmop.Name))
		})
	})

	When("HelmOp does not have user ID label", func() {
		BeforeEach(func() {
			createHelmOp("test-helmop-without-userid", nil)
		})

		It("does not include userID in log output", func() {
			Eventually(logsBuffer.String, timeout).Should(ContainSubstring(helmop.Name))

			logs := logsBuffer.String()
			helmopLogs := utils.ExtractResourceLogs(logs, helmop.Name)
			Expect(helmopLogs).NotTo(ContainSubstring(`"userID"`))
		})
	})
})
