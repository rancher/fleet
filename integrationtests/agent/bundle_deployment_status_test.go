package agent_test

import (
	"context"
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
		Expect(err).ToNot(HaveOccurred())
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

		It("Updates the bundle deployment's status", func() {
			By("Detecting the BundleDeployment as not ready")
			bd := &v1alpha1.BundleDeployment{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
			Expect(err).NotTo(HaveOccurred())
			Expect(bd.Status.Ready).To(BeFalse())

			By("Eventually updating the BundleDeployment to make it ready and non modified")
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())

			By("Deploying resources from the BundleDeployment to the cluster")
			svc, err := env.getService(svcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.Name).NotTo(BeEmpty())

			By("Listing deployed resources in the status")
			bd = &v1alpha1.BundleDeployment{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
			Expect(err).NotTo(HaveOccurred())
			Expect(bd.Status.Resources).To(HaveLen(4))
			ts := bd.Status.Resources[0].CreatedAt
			Expect(ts.Time).ToNot(BeZero())
			Expect(bd.Status.Resources).To(ContainElement(v1alpha1.BundleDeploymentResource{
				Kind:       "Service",
				APIVersion: "v1",
				Namespace:  namespace,
				Name:       svcName,
				CreatedAt:  ts,
			}))
		})

		Context("A release resource is modified", func() {
			It("Updates the bundle deployment's status", func() {
				By("Receiving an update on a service in the bundle deployment")
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				patch := svc.DeepCopy()
				patch.Spec.Selector = map[string]string{"foo": "bar"}
				Expect(k8sClient.Patch(ctx, patch, client.MergeFrom(&svc))).NotTo(HaveOccurred())

				By("Updating the BundleDeployment status as not Ready, and containing an error message")
				Eventually(func(g Gomega) {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  namespace,
						Name:       svcName,
						Create:     false,
						Delete:     false,
						Patch:      "{\"spec\":{\"selector\":{\"app.kubernetes.io/name\":\"MyApp\"}}}",
					}
					env.isNotReadyAndModified(
						g,
						name,
						modifiedStatus,
						"service.v1 "+namespace+"/svc-test modified {\"spec\":{\"selector\":{\"app.kubernetes.io/name\":\"MyApp\"}}}",
					)
				}).Should(Succeed())

				By("Modifying the service to have its original value")
				svc, err = env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				patch = svc.DeepCopy()
				patch.Spec.Selector = map[string]string{"app.kubernetes.io/name": "MyApp"}
				Expect(k8sClient.Patch(ctx, patch, client.MergeFrom(&svc))).To(Succeed())

				By("Eventually updating the BundleDeployment status to ready and non modified")
				Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
			})
		})

		Context("Upgrading to a release that will leave an orphan resource", func() {
			It("Updates the bundle deployment's status", func() {
				By("Receiving an update to a release that deletes the svc with a finalizer")
				Eventually(func() bool {
					bd := &v1alpha1.BundleDeployment{}
					err := k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
					Expect(err).ToNot(HaveOccurred())
					bd.Spec.DeploymentID = "v2"
					err = k8sClient.Update(ctx, bd)
					return err == nil && bd != nil
				}).Should(BeTrue())

				By("Eventually updating the BundleDeployment status to be extra")
				Eventually(func(g Gomega) {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  namespace,
						Name:       svcFinalizerName,
						Create:     false,
						Delete:     true,
						Patch:      "",
					}
					env.isNotReadyAndModified(
						g,
						name,
						modifiedStatus,
						"service.v1 "+namespace+"/svc-finalizer extra",
					)
				}, timeout, 20*time.Millisecond).Should(Succeed())

				By("Removing the finalizer")
				svc, err := env.getService(svcFinalizerName)
				Expect(err).NotTo(HaveOccurred())
				patch := svc.DeepCopy()
				patch.Finalizers = nil
				Expect(k8sClient.Patch(ctx, patch, client.MergeFrom(&svc))).To(Succeed())

				By("Eventually updating the BundleDeployment status to ready and non modified")
				Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
			})
		})

		Context("Delete a resource in the release", func() {
			It("Updates the deployment", func() {
				By("Receiving an update deleting a resource from the release")
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				err = k8sClient.Delete(ctx, &svc)
				Expect(err).NotTo(HaveOccurred())

				By("Eventually updating the BundleDeployment status to show the resource as missing")
				Eventually(func(g Gomega) {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  namespace,
						Name:       svcName,
						Create:     true,
						Delete:     false,
						Patch:      "",
					}
					env.isNotReadyAndModified(
						g,
						name,
						modifiedStatus,
						"service.v1 "+namespace+"/svc-test missing",
					)
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

		It("Updates the bundle deployment's status", func() {
			By("Showing the BundleDeployment's Ready status as false")
			bd := &v1alpha1.BundleDeployment{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
			Expect(err).NotTo(HaveOccurred())
			Expect(bd.Status.Ready).To(BeFalse())

			By("Eventually showing the BundleDeployment as ready and non modified")
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())

			By("Including resources from BundleDeployment in the cluster")
			svc, err := env.getService(svcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.Name).NotTo(BeEmpty())

			By("Listing deployed resources in the status")
			bd = &v1alpha1.BundleDeployment{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
			Expect(err).NotTo(HaveOccurred())
			Expect(bd.Status.Resources).To(HaveLen(4))
			ts := bd.Status.Resources[0].CreatedAt
			Expect(ts.Time).ToNot(BeZero())
			Expect(bd.Status.Resources).To(ContainElement(v1alpha1.BundleDeploymentResource{
				Kind:       "Service",
				APIVersion: "v1",
				Namespace:  namespace,
				Name:       svcName,
				CreatedAt:  ts,
			}))
		})

		Context("A release resource is modified", func() {
			It("Updates the bundle deployment's status", func() {
				By("Receiving an update on the resource")
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				patch := svc.DeepCopy()
				patch.Spec.Selector = map[string]string{"foo": "bar"}
				Expect(k8sClient.Patch(ctx, patch, client.MergeFrom(&svc))).NotTo(HaveOccurred())

				By("Eventually updating the BundleDeployment status as not Ready, containing the error message")
				Eventually(func(g Gomega) {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  namespace,
						Name:       svcName,
						Create:     false,
						Delete:     false,
						Patch:      "{\"spec\":{\"selector\":{\"app.kubernetes.io/name\":\"MyApp\"}}}",
					}
					env.isNotReadyAndModified(
						g,
						name,
						modifiedStatus,
						"service.v1 "+namespace+"/svc-test modified {\"spec\":{\"selector\":{\"app.kubernetes.io/name\":\"MyApp\"}}}",
					)
				}).Should(Succeed())

				By("Receiving an update to the resource, restoring its original state")
				svc, err = env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				patch = svc.DeepCopy()
				patch.Spec.Selector = map[string]string{"app.kubernetes.io/name": "MyApp"}
				Expect(k8sClient.Patch(ctx, patch, client.MergeFrom(&svc))).To(Succeed())

				By("Eventually showing the BundleDeployment as ready and non modified")
				Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
			})
		})

		Context("Upgrading to a release that will leave an orphan resource", func() {
			It("Updates the bundle deployment's status", func() {
				By("Receiving a BundleDeployment upgrade to a release that deletes the svc with a finalizer")
				Eventually(func() bool {
					bd := &v1alpha1.BundleDeployment{}
					err := k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
					Expect(err).ToNot(HaveOccurred())
					bd.Spec.DeploymentID = "v2"
					err = k8sClient.Update(ctx, bd)
					return err == nil && bd != nil
				}).Should(BeTrue())

				By("Eventually updating the BundleDeployment status to be extra")
				Eventually(func(g Gomega) {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  namespace,
						Name:       svcFinalizerName,
						Create:     false,
						Delete:     true,
						Patch:      "",
					}
					env.isNotReadyAndModified(
						g,
						name,
						modifiedStatus,
						"service.v1 "+namespace+"/svc-finalizer extra",
					)
				}, timeout, 20*time.Millisecond).Should(Succeed())

				By("Removing the finalizer")
				svc, err := env.getService(svcFinalizerName)
				Expect(err).NotTo(HaveOccurred())
				patch := svc.DeepCopy()
				patch.Finalizers = nil
				Expect(k8sClient.Patch(ctx, patch, client.MergeFrom(&svc))).To(Succeed())

				By("Eventually updating the BundleDeployment status to ready and non modified")
				Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
			})
		})

		Context("Delete a resource in the release", func() {
			It("Updates the bundle deployment's status", func() {
				By("Receiving an update deleting a resource from the release")
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				err = k8sClient.Delete(ctx, &svc)
				Expect(err).NotTo(HaveOccurred())

				By("Eventually updating the BundleDeployment status to show the resource as missing")
				Eventually(func(g Gomega) {
					modifiedStatus := v1alpha1.ModifiedStatus{
						Kind:       "Service",
						APIVersion: "v1",
						Namespace:  namespace,
						Name:       svcName,
						Create:     true,
						Delete:     false,
						Patch:      "",
					}
					env.isNotReadyAndModified(
						g,
						name,
						modifiedStatus,
						"service.v1 "+namespace+"/svc-test missing",
					)
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

		It("Updates the bundle deployment's status", func() {
			By("Eventually updating the BundleDeployment status to ready and non modified")
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())

			By("Including resources from BundleDeployment in the cluster")
			svc, err := env.getService(svcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.Name).NotTo(BeEmpty())
		})
	})
})
