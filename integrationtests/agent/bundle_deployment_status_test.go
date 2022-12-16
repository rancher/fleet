package agent

import (
	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/pkg/genericcondition"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

const (
	bundle           = "bundle"
	timeout          = 5 * time.Second
	svcName          = "svc-test"
	svcFinalizerName = "svc-finalizer"
)

var _ = Describe("BundleDeployment status", Ordered, func() {
	When("New bundle deployment is created", func() {
		BeforeAll(func() {
			// this BundleDeployment will create a deployment with the resources from assets/deployment.yaml
			createBundleDeploymentV1()
		})
		AfterAll(func() {
			Expect(controller.Delete(DeploymentsNamespace, bundle, nil)).NotTo(HaveOccurred())
		})
		It("BundleDeployment is not ready", func() {
			bd, err := controller.Get(DeploymentsNamespace, bundle, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(bd.Status.Ready).To(BeFalse())
		})
		It("BundleDeployment will eventually be ready and non modified", func() {
			Eventually(isBundleDeploymentReadyAndNotModified).WithTimeout(timeout).Should(BeTrue())
		})
		It("Resources from BundleDeployment are present in the cluster", func() {
			svc, err := getServiceFromBundleDeploymentRelease(svcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc).To(Not(BeNil()))
		})
		Context("A release resource is modified", func() {
			It("Modify service", func() {
				svc, err := getServiceFromBundleDeploymentRelease(svcName)
				Expect(err).NotTo(HaveOccurred())
				patch := svc.DeepCopy()
				patch.Spec.Selector = map[string]string{"foo": "bar"}
				Expect(k8sClient.Patch(ctx, patch, client.MergeFrom(&svc))).NotTo(HaveOccurred())
			})
			It("BundleDeployment status will not be Ready, and will contain the error message", func() {
				Eventually(func() bool {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  DeploymentsNamespace,
						Name:       "svc-test",
						Create:     false,
						Delete:     false,
						Patch:      "{\"spec\":{\"selector\":{\"app.kubernetes.io/name\":\"MyApp\"}}}",
					}
					return isNotReadyAndModified(modifiedStatus, "service.v1 fleet-integration-tests/svc-test modified {\"spec\":{\"selector\":{\"app.kubernetes.io/name\":\"MyApp\"}}}")
				}).WithTimeout(timeout).Should(BeTrue())
			})
			It("Modify service to have its original value", func() {
				svc, err := getServiceFromBundleDeploymentRelease(svcName)
				Expect(err).NotTo(HaveOccurred())
				patch := svc.DeepCopy()
				patch.Spec.Selector = map[string]string{"app.kubernetes.io/name": "MyApp"}
				Expect(k8sClient.Patch(ctx, patch, client.MergeFrom(&svc))).To(BeNil())
			})
			It("BundleDeployment will eventually be ready and non modified", func() {
				Eventually(isBundleDeploymentReadyAndNotModified).WithTimeout(timeout).Should(BeTrue())
			})
		})
		Context("Upgrading to a release that will leave an orphan resource", func() {
			It("Upgrade BundleDeployment to a release that deletes the svc with a finalizer", func() {
				upgradeBundleDeploymentToV2()
			})
			It("BundleDeployment status will eventually be extra", func() {
				Eventually(func() bool {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  DeploymentsNamespace,
						Name:       "svc-finalizer",
						Create:     false,
						Delete:     true,
						Patch:      "",
					}
					return isNotReadyAndModified(modifiedStatus, "service.v1 fleet-integration-tests/svc-finalizer extra")
				}).WithTimeout(timeout).Should(BeTrue())
			})
			It("Remove finalizer", func() {
				svc, err := getServiceFromBundleDeploymentRelease(svcFinalizerName)
				Expect(err).NotTo(HaveOccurred())
				patch := svc.DeepCopy()
				patch.Finalizers = nil
				Expect(k8sClient.Patch(ctx, patch, client.MergeFrom(&svc))).To(BeNil())
			})
			It("BundleDeployment will eventually be ready and non modified", func() {
				Eventually(isBundleDeploymentReadyAndNotModified).WithTimeout(timeout).Should(BeTrue())
			})
		})
		Context("Delete a resource in the release", func() {
			It("Delete service", func() {
				svc, err := getServiceFromBundleDeploymentRelease(svcName)
				Expect(err).NotTo(HaveOccurred())
				err = k8sClient.Delete(ctx, &svc)
				Expect(err).NotTo(HaveOccurred())
			})
			It("BundleDeployment status will eventually be missing", func() {
				Eventually(func() bool {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  DeploymentsNamespace,
						Name:       "svc-test",
						Create:     true,
						Delete:     false,
						Patch:      "",
					}
					return isNotReadyAndModified(modifiedStatus, "service.v1 fleet-integration-tests/svc-test missing")
				}).WithTimeout(timeout).Should(BeTrue())
			})
		})
	})
})

func isNotReadyAndModified(modifiedStatus v1alpha1.ModifiedStatus, message string) bool {
	bd, err := controller.Get(DeploymentsNamespace, "bundle", metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	isReadyCondition := checkCondition(bd.Status.Conditions, "Ready", "False", message)

	return cmp.Equal(bd.Status.ModifiedStatus, []v1alpha1.ModifiedStatus{modifiedStatus}) &&
		!bd.Status.NonModified &&
		isReadyCondition
}

func isBundleDeploymentReadyAndNotModified() bool {
	bd, err := controller.Get(DeploymentsNamespace, bundle, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	return bd.Status.Ready && bd.Status.NonModified
}

func getServiceFromBundleDeploymentRelease(name string) (corev1.Service, error) {
	nsn := types.NamespacedName{
		Name:      name,
		Namespace: DeploymentsNamespace,
	}
	cm := corev1.Service{}
	err := k8sClient.Get(ctx, nsn, &cm)
	if err != nil {
		return corev1.Service{}, err
	}

	return cm, nil
}

func createBundleDeploymentV1() {
	bundled := v1alpha1.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bundle,
			Namespace: DeploymentsNamespace,
		},
		Spec: v1alpha1.BundleDeploymentSpec{
			DeploymentID: "v1",
		},
	}

	b, err := controller.Create(&bundled)
	Expect(err).To(BeNil())
	Expect(b).To(Not(BeNil()))
}

func upgradeBundleDeploymentToV2() {
	bd, err := controller.Get(DeploymentsNamespace, bundle, metav1.GetOptions{})
	Expect(err).To(BeNil())
	bd.Spec.DeploymentID = "v2"
	b, err := controller.Update(bd)
	Expect(err).To(BeNil())
	Expect(b).To(Not(BeNil()))
}

func checkCondition(conditions []genericcondition.GenericCondition, conditionType string, status string, message string) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType && string(condition.Status) == status && condition.Message == message {
			return true
		}
	}

	return false
}
