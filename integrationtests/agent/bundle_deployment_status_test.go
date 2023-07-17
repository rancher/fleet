package agent

import (
	"os"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func orphanBundeResources() map[string][]v1alpha1.BundleResource {
	v1, err := os.ReadFile(assetsPath + "/deployment-v1.yaml")
	Expect(err).NotTo(HaveOccurred())
	v2, err := os.ReadFile(assetsPath + "/deployment-v2.yaml")
	Expect(err).NotTo(HaveOccurred())

	return map[string][]v1alpha1.BundleResource{
		"v1": {
			{
				Name:     "deployment-v1.yaml",
				Content:  string(v1),
				Encoding: "",
			},
		}, "v2": {
			{
				Name:     "deployment-v2.yaml",
				Content:  string(v2),
				Encoding: "",
			},
		},
	}
}

var _ = Describe("BundleDeployment status", Ordered, func() {

	const (
		svcName          = "svc-test"
		svcFinalizerName = "svc-finalizer"
	)

	var (
		env       *specEnv
		namespace string
		name      string
	)

	createBundleDeploymentV1 := func(name string) {
		bundled := v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: v1alpha1.BundleDeploymentSpec{
				DeploymentID: "v1",
			},
		}

		b, err := env.controller.Create(&bundled)
		Expect(err).To(BeNil())
		Expect(b).To(Not(BeNil()))
	}

	BeforeAll(func() {
		env = specEnvs["orphanbundle"]
		name = "orphanbundle"
		namespace = env.namespace
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).ToNot(HaveOccurred())
		})
	})

	When("New bundle deployment is created", func() {
		BeforeAll(func() {
			// this BundleDeployment will create a deployment with the resources from assets/deployment-v1.yaml
			createBundleDeploymentV1(name)
		})

		AfterAll(func() {
			Expect(env.controller.Delete(namespace, name, nil)).NotTo(HaveOccurred())
		})

		It("BundleDeployment is not ready", func() {
			bd, err := env.controller.Get(namespace, name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(bd.Status.Ready).To(BeFalse())
		})

		It("BundleDeployment will eventually be ready and non modified", func() {
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
		})

		It("Resources from BundleDeployment are present in the cluster", func() {
			svc, err := env.getService(svcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc).To(Not(BeNil()))
		})

		It("Lists deployed resources in the status", func() {
			bd, err := env.controller.Get(namespace, name, metav1.GetOptions{})
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

		Context("A release resource is modified", func() {
			It("Modify service", func() {
				svc, err := env.getService(svcName)
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
						Namespace:  namespace,
						Name:       "svc-test",
						Create:     false,
						Delete:     false,
						Patch:      "{\"spec\":{\"selector\":{\"app.kubernetes.io/name\":\"MyApp\"}}}",
					}
					return env.isNotReadyAndModified(name, modifiedStatus, "service.v1 "+namespace+"/svc-test modified {\"spec\":{\"selector\":{\"app.kubernetes.io/name\":\"MyApp\"}}}")
				}).Should(BeTrue())
			})

			It("Modify service to have its original value", func() {
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				patch := svc.DeepCopy()
				patch.Spec.Selector = map[string]string{"app.kubernetes.io/name": "MyApp"}
				Expect(k8sClient.Patch(ctx, patch, client.MergeFrom(&svc))).To(BeNil())
			})

			It("BundleDeployment will eventually be ready and non modified", func() {
				Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
			})
		})

		Context("Upgrading to a release that will leave an orphan resource", func() {
			It("Upgrade BundleDeployment to a release that deletes the svc with a finalizer", func() {
				Eventually(func() bool {
					bd, err := env.controller.Get(namespace, name, metav1.GetOptions{})
					Expect(err).To(BeNil())
					bd.Spec.DeploymentID = "v2"
					b, err := env.controller.Update(bd)
					return err == nil && b != nil
				}).Should(BeTrue())

			})

			It("BundleDeployment status will eventually be extra", func() {
				Eventually(func() bool {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  namespace,
						Name:       "svc-finalizer",
						Create:     false,
						Delete:     true,
						Patch:      "",
					}
					return env.isNotReadyAndModified(name, modifiedStatus, "service.v1 "+namespace+"/svc-finalizer extra")
				}).Should(BeTrue())
			})

			It("Remove finalizer", func() {
				svc, err := env.getService(svcFinalizerName)
				Expect(err).NotTo(HaveOccurred())
				patch := svc.DeepCopy()
				patch.Finalizers = nil
				Expect(k8sClient.Patch(ctx, patch, client.MergeFrom(&svc))).To(BeNil())
			})

			It("BundleDeployment will eventually be ready and non modified", func() {
				Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
			})
		})

		Context("Delete a resource in the release", func() {
			It("Delete service", func() {
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				err = k8sClient.Delete(ctx, &svc)
				Expect(err).NotTo(HaveOccurred())
			})

			It("BundleDeployment status will eventually be missing", func() {
				Eventually(func() bool {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  namespace,
						Name:       "svc-test",
						Create:     true,
						Delete:     false,
						Patch:      "",
					}
					return env.isNotReadyAndModified(name, modifiedStatus, "service.v1 "+namespace+"/svc-test missing")
				}).Should(BeTrue())
			})
		})
	})

	When("Simulating how another operator creates a dynamic resource", func() {
		BeforeAll(func() {
			createBundleDeploymentV1(name)
			// It is possible that some operators copy the objectset.rio.cattle.io/hash label into a dynamically created objects.
			// https://github.com/rancher/fleet/issues/1141
			By("Simulating orphan resource creation", func() {
				newSvc := corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "svc-new",
						Namespace: namespace,
						Labels:    map[string]string{"objectset.rio.cattle.io/hash": "108df84396abb3afdcdcf511abdd40c2cb8d5beb"},
					},
					Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{
						Protocol:   "TCP",
						Port:       2,
						TargetPort: intstr.FromInt(1),
					}}},
				}

				err := k8sClient.Create(ctx, &newSvc)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		AfterAll(func() {
			Expect(env.controller.Delete(namespace, name, nil)).NotTo(HaveOccurred())
		})

		It("BundleDeployment will eventually be ready and non modified", func() {
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
		})

		It("Resources from BundleDeployment are present in the cluster", func() {
			svc, err := env.getService(svcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc).To(Not(BeNil()))
		})
	})
})
