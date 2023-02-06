package agent

import (
	"os"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetgen "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("BundleDeployment status", Ordered, func() {

	const (
		bundle           = "bundle"
		svcName          = "svc-test"
		svcFinalizerName = "svc-finalizer"
	)

	var (
		controller fleetgen.BundleDeploymentController
		env        specEnv
		namespace  string
	)

	createBundleDeploymentV1 := func() {
		bundled := v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      bundle,
				Namespace: namespace,
			},
			Spec: v1alpha1.BundleDeploymentSpec{
				DeploymentID: "v1",
			},
		}

		b, err := controller.Create(&bundled)
		Expect(err).To(BeNil())
		Expect(b).To(Not(BeNil()))
	}

	createResources := func() (map[string][]v1alpha1.BundleResource, error) {
		v1, err := os.ReadFile(assetsPath + "/deployment-v1.yaml")
		if err != nil {
			return nil, err
		}
		v2, err := os.ReadFile(assetsPath + "/deployment-v2.yaml")
		if err != nil {
			return nil, err
		}

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
		}, nil
	}

	BeforeAll(func() {
		namespace = newNamespaceName()
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		})).NotTo(HaveOccurred())

		resources, err := createResources()
		Expect(err).ToNot(HaveOccurred())
		controller = registerBundleDeploymentController(cfg, namespace, newLookup((resources)))

		env = specEnv{controller: controller, k8sClient: k8sClient, namespace: namespace, name: bundle}
	})

	When("New bundle deployment is created", func() {
		BeforeAll(func() {
			// this BundleDeployment will create a deployment with the resources from assets/deployment-v1.yaml
			createBundleDeploymentV1()
		})

		AfterAll(func() {
			Expect(controller.Delete(namespace, bundle, nil)).NotTo(HaveOccurred())
		})

		It("BundleDeployment is not ready", func() {
			bd, err := controller.Get(namespace, bundle, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(bd.Status.Ready).To(BeFalse())
		})

		It("BundleDeployment will eventually be ready and non modified", func() {
			Eventually(env.isBundleDeploymentReadyAndNotModified).Should(BeTrue())
		})

		It("Resources from BundleDeployment are present in the cluster", func() {
			svc, err := env.getService(svcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc).To(Not(BeNil()))
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
					return env.isNotReadyAndModified(modifiedStatus, "service.v1 "+namespace+"/svc-test modified {\"spec\":{\"selector\":{\"app.kubernetes.io/name\":\"MyApp\"}}}")
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
				Eventually(env.isBundleDeploymentReadyAndNotModified).Should(BeTrue())
			})
		})

		Context("Upgrading to a release that will leave an orphan resource", func() {
			It("Upgrade BundleDeployment to a release that deletes the svc with a finalizer", func() {
				bd, err := controller.Get(namespace, bundle, metav1.GetOptions{})
				Expect(err).To(BeNil())
				bd.Spec.DeploymentID = "v2"
				b, err := controller.Update(bd)
				Expect(err).To(BeNil())
				Expect(b).To(Not(BeNil()))
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
					return env.isNotReadyAndModified(modifiedStatus, "service.v1 "+namespace+"/svc-finalizer extra")
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
				Eventually(env.isBundleDeploymentReadyAndNotModified).Should(BeTrue())
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
					return env.isNotReadyAndModified(modifiedStatus, "service.v1 "+namespace+"/svc-test missing")
				}).Should(BeTrue())
			})
		})
	})

	When("simulate operator dynamic resource creation", func() {
		BeforeAll(func() {
			createBundleDeploymentV1()
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
			Expect(controller.Delete(namespace, bundle, nil)).NotTo(HaveOccurred())
		})

		It("BundleDeployment will eventually be ready and non modified", func() {
			Eventually(env.isBundleDeploymentReadyAndNotModified).Should(BeTrue())
		})

		It("Resources from BundleDeployment are present in the cluster", func() {
			svc, err := env.getService(svcName)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc).To(Not(BeNil()))
		})
	})
})
