package agent

import (
	"context"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("BundleDeployment drift correction", Ordered, func() {

	const svcName = "svc-test"

	var (
		namespace string
		name      string
		env       *specEnv
	)

	createBundleDeployment := func(name string) {
		bundled := v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: clusterNS,
			},
			Spec: v1alpha1.BundleDeploymentSpec{
				DeploymentID: "v1",
				Options: v1alpha1.BundleDeploymentOptions{
					DefaultNamespace: namespace,
					CorrectDrift: &v1alpha1.CorrectDrift{
						Enabled: true,
					},
					Helm: &v1alpha1.HelmOptions{
						MaxHistory: 2,
					},
				},
				CorrectDrift: &v1alpha1.CorrectDrift{
					Enabled: true,
				},
			},
		}

		err := k8sClient.Create(context.TODO(), &bundled)
		Expect(err).To(BeNil())
		Expect(bundled).To(Not(BeNil()))
	}

	createNamespace := func() string {
		newNs, err := utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: newNs}}
		Expect(k8sClient.Create(context.Background(), ns)).ToNot(HaveOccurred())

		return newNs
	}

	When("Simulating drift", func() {
		BeforeAll(func() {
			namespace = createNamespace()
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: namespace}})).ToNot(HaveOccurred())
			})
			env = &specEnv{namespace: namespace}

			name = "drift-test"
			createBundleDeployment(name)
			DeferCleanup(func() {
				Expect(k8sClient.Delete(context.TODO(), &v1alpha1.BundleDeployment{
					ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: name},
				})).ToNot(HaveOccurred())
			})
		})

		It("Deploys a bundle deployment which is not ready while its resources are being deployed", func() {
			bd := &v1alpha1.BundleDeployment{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
			Expect(err).NotTo(HaveOccurred())
			Expect(bd.Status.Ready).To(BeFalse())
		})

		It("Updates the bundle deployment which will eventually be ready and non modified", func() {
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
		})

		It("Creates resources from the bundle deployment in the cluster", func() {
			svc, err := env.getService(svcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.Name).NotTo(BeEmpty())
		})

		It("Lists deployed resources in the bundle deployment status", func() {
			bd := &v1alpha1.BundleDeployment{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
			Expect(err).NotTo(HaveOccurred())
			Expect(bd.Status.Resources).To(HaveLen(3))
			ts := bd.Status.Resources[0].CreatedAt
			Expect(ts.Time).ToNot(BeZero())
			Expect(bd.Status.Resources).To(ContainElement(v1alpha1.BundleDeploymentResource{
				Kind:       "Service",
				APIVersion: "v1",
				Namespace:  namespace,
				Name:       "svc-test",
				CreatedAt:  ts,
			}))
		})

		Context("A release resource is modified", Ordered, func() {
			It("Receives a modification on a service", func() {
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				patchedSvc := svc.DeepCopy()
				patchedSvc.Spec.Ports[0].TargetPort = intstr.FromInt(4242)
				patchedSvc.Spec.Ports[0].Port = 4242
				patchedSvc.Spec.Ports[0].Name = "myport"
				Expect(k8sClient.Patch(ctx, patchedSvc, client.StrategicMergeFrom(&svc))).NotTo(HaveOccurred())
			})

			It("Updates the BundleDeployment status as not Ready, including the error message", func() {
				Eventually(func(g Gomega) {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  namespace,
						Name:       "svc-test",
						Create:     false,
						Delete:     false,
						Patch:      `{"spec":{"ports":[{"name":"myport","port":80,"protocol":"TCP","targetPort":9376},{"name":"myport","port":4242,"protocol":"TCP","targetPort":4242}]}}`,
					}
					isOK, status := env.isNotReadyAndModified(
						name,
						modifiedStatus,
						`cannot patch "svc-test" with kind Service: Service "svc-test" is invalid: spec.ports[1].name: Duplicate value: "myport"`,
					)
					g.Expect(isOK).To(BeTrue(), status)
				}).Should(Succeed())
			})
		})
	})
})
