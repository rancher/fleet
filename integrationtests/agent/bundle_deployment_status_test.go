package agent_test

import (
	"context"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func init() {
	v1, _ := os.ReadFile(assetsPath + "/deployment-v1.yaml")
	v2, _ := os.ReadFile(assetsPath + "/deployment-v2.yaml")

	resources["v1"] = []v1alpha1.BundleResource{
		{
			Name:     "deployment-v1.yaml",
			Content:  string(v1),
			Encoding: "",
		},
	}
	resources["v2"] = []v1alpha1.BundleResource{
		{
			Name:     "deployment-v2.yaml",
			Content:  string(v2),
			Encoding: "",
		},
	}
}

var _ = Describe("BundleDeployment status", Ordered, func() {

	const (
		svcName          = "svc-test"
		svcFinalizerName = "svc-finalizer"
	)

	var (
		namespace string
		name      string
		env       *specEnv
	)

	createBundleDeploymentV1 := func(name string) {
		bundled := v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: clusterNS,
			},
			Spec: v1alpha1.BundleDeploymentSpec{
				DeploymentID: "v1",
				Options: v1alpha1.BundleDeploymentOptions{
					DefaultNamespace: namespace,
				},
			},
		}

		err := k8sClient.Create(context.TODO(), &bundled)
		Expect(err).To(BeNil())
		Expect(bundled).To(Not(BeNil()))
	}

	When("New bundle deployment is created", func() {
		BeforeAll(func() {
			name = "orphanbundletest1"
			namespace = createNamespace()
			env = &specEnv{namespace: namespace}

			// this BundleDeployment will create a deployment with the resources from assets/deployment-v1.yaml
			createBundleDeploymentV1(name)
			DeferCleanup(func() {
				Expect(k8sClient.Delete(context.TODO(), &v1alpha1.BundleDeployment{
					ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: name},
				})).ToNot(HaveOccurred())
			})
		})

		It("Detects the BundleDeployment as not ready", func() {
			bd := &v1alpha1.BundleDeployment{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
			Expect(err).NotTo(HaveOccurred())
			Expect(bd.Status.Ready).To(BeFalse())
		})

		It("Eventually updates the BundleDeployment to make it ready and non modified", func() {
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
		})

		It("Deploys resources from BundleDeployment to the cluster", func() {
			svc, err := env.getService(svcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.Name).NotTo(BeEmpty())
		})

		It("Lists deployed resources in the status", func() {
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

		Context("A release resource is modified", func() {
			It("Modify service", func() {
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				patch := svc.DeepCopy()
				patch.Spec.Selector = map[string]string{"foo": "bar"}
				Expect(k8sClient.Patch(ctx, patch, client.MergeFrom(&svc))).NotTo(HaveOccurred())
			})

			It("BundleDeployment status will not be Ready, and will contain the error message", func() {
				Eventually(func(g Gomega) {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  namespace,
						Name:       "svc-test",
						Create:     false,
						Delete:     false,
						Patch:      "{\"spec\":{\"selector\":{\"app.kubernetes.io/name\":\"MyApp\"}}}",
					}
					isOK, status := env.isNotReadyAndModified(
						name,
						modifiedStatus,
						"service.v1 "+namespace+"/svc-test modified {\"spec\":{\"selector\":{\"app.kubernetes.io/name\":\"MyApp\"}}}",
					)
					g.Expect(isOK).To(BeTrue(), status)
				}).Should(Succeed())
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
					bd := &v1alpha1.BundleDeployment{}
					err := k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
					Expect(err).To(BeNil())
					bd.Spec.DeploymentID = "v2"
					err = k8sClient.Update(ctx, bd)
					return err == nil && bd != nil
				}).Should(BeTrue())

			})

			It("BundleDeployment status will eventually be extra", func() {
				Eventually(func(g Gomega) {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  namespace,
						Name:       "svc-finalizer",
						Create:     false,
						Delete:     true,
						Patch:      "",
					}
					isOK, status := env.isNotReadyAndModified(
						name,
						modifiedStatus,
						"service.v1 "+namespace+"/svc-finalizer extra",
					)

					g.Expect(isOK).To(BeTrue(), status)
				}, timeout, 20*time.Millisecond).Should(Succeed())
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
				Eventually(func(g Gomega) {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  namespace,
						Name:       "svc-test",
						Create:     true,
						Delete:     false,
						Patch:      "",
					}
					isOK, status := env.isNotReadyAndModified(
						name,
						modifiedStatus,
						"service.v1 "+namespace+"/svc-test missing",
					)

					g.Expect(isOK).To(BeTrue(), status)
				}).Should(Succeed())
			})
		})
	})

	When("Simulating how another operator modifies a dynamic resource", func() {
		BeforeAll(func() {
			namespace = createNamespace()
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: namespace}})).ToNot(HaveOccurred())
			})
			env = &specEnv{namespace: namespace}

			name = "orphanbundletest2"
			createBundleDeploymentV1(name)
			DeferCleanup(func() {
				Expect(k8sClient.Delete(context.TODO(), &v1alpha1.BundleDeployment{
					ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: name},
				})).ToNot(HaveOccurred())
			})
		})

		It("BundleDeployment is not ready", func() {
			bd := &v1alpha1.BundleDeployment{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
			Expect(err).NotTo(HaveOccurred())
			Expect(bd.Status.Ready).To(BeFalse())
		})

		It("BundleDeployment will eventually be ready and non modified", func() {
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
		})

		It("Resources from BundleDeployment are present in the cluster", func() {
			svc, err := env.getService(svcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.Name).NotTo(BeEmpty())
		})

		It("Lists deployed resources in the status", func() {
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

		Context("A release resource is modified", func() {
			It("Modify service", func() {
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				patch := svc.DeepCopy()
				patch.Spec.Selector = map[string]string{"foo": "bar"}
				Expect(k8sClient.Patch(ctx, patch, client.MergeFrom(&svc))).NotTo(HaveOccurred())
			})

			It("BundleDeployment status will not be Ready, and will contain the error message", func() {
				Eventually(func(g Gomega) {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  namespace,
						Name:       "svc-test",
						Create:     false,
						Delete:     false,
						Patch:      "{\"spec\":{\"selector\":{\"app.kubernetes.io/name\":\"MyApp\"}}}",
					}
					isOK, status := env.isNotReadyAndModified(
						name,
						modifiedStatus,
						"service.v1 "+namespace+"/svc-test modified {\"spec\":{\"selector\":{\"app.kubernetes.io/name\":\"MyApp\"}}}",
					)

					g.Expect(isOK).To(BeTrue(), status)
				}).Should(Succeed())
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
					bd := &v1alpha1.BundleDeployment{}
					err := k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
					Expect(err).To(BeNil())
					bd.Spec.DeploymentID = "v2"
					err = k8sClient.Update(ctx, bd)
					return err == nil && bd != nil
				}).Should(BeTrue())

			})

			It("BundleDeployment status will eventually be extra", func() {
				Eventually(func(g Gomega) {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  namespace,
						Name:       "svc-finalizer",
						Create:     false,
						Delete:     true,
						Patch:      "",
					}
					isOK, status := env.isNotReadyAndModified(
						name,
						modifiedStatus,
						"service.v1 "+namespace+"/svc-finalizer extra",
					)

					g.Expect(isOK).To(BeTrue(), status)
				}, timeout, 20*time.Millisecond).Should(Succeed())
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
				Eventually(func(g Gomega) {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  namespace,
						Name:       "svc-test",
						Create:     true,
						Delete:     false,
						Patch:      "",
					}
					isOK, status := env.isNotReadyAndModified(
						name,
						modifiedStatus,
						"service.v1 "+namespace+"/svc-test missing",
					)

					g.Expect(isOK).To(BeTrue(), status)
				}).Should(Succeed())
			})
		})
	})

	When("Simulating how another operator creates a dynamic resource", func() {
		BeforeAll(func() {
			namespace = createNamespace()
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: namespace}})).ToNot(HaveOccurred())
			})
			env = &specEnv{namespace: namespace}

			name = "orphanbundletest2"
			createBundleDeploymentV1(name)
			DeferCleanup(func() {
				Expect(k8sClient.Delete(context.TODO(), &v1alpha1.BundleDeployment{
					ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: name},
				})).ToNot(HaveOccurred())
			})

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

		It("BundleDeployment will eventually be ready and non modified", func() {
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
		})

		It("Resources from BundleDeployment are present in the cluster", func() {
			svc, err := env.getService(svcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.Name).NotTo(BeEmpty())
		})
	})
})
