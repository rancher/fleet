package agent_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("BundleDeployment drift correction", Ordered, func() {

	const svcName = "svc-test"

	var (
		namespace    string
		name         string
		deplID       string
		env          *specEnv
		correctDrift v1alpha1.CorrectDrift
	)

	createBundleDeployment := func(name string) {
		bundled := v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: clusterNS,
			},
			Spec: v1alpha1.BundleDeploymentSpec{
				DeploymentID: deplID,
				Options: v1alpha1.BundleDeploymentOptions{
					DefaultNamespace: namespace,
					CorrectDrift:     &correctDrift,
					Helm: &v1alpha1.HelmOptions{
						MaxHistory: 2,
					},
				},
				CorrectDrift: &correctDrift,
			},
		}

		err := k8sClient.Create(context.TODO(), &bundled)
		Expect(err).ToNot(HaveOccurred())
		Expect(bundled).To(Not(BeNil()))
	}

	createNamespace := func() string {
		newNs, err := utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: newNs}}
		Expect(k8sClient.Create(context.Background(), ns)).ToNot(HaveOccurred())

		return newNs
	}

	When("Drift correction is not enabled", func() {
		BeforeAll(func() {
			namespace = createNamespace()
			deplID = "v1"
			correctDrift = v1alpha1.CorrectDrift{Enabled: false}
			env = &specEnv{namespace: namespace}

			name = "drift-disabled-test"
			createBundleDeployment(name)
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())

			DeferCleanup(func() {
				Expect(k8sClient.Delete(context.TODO(), &v1alpha1.BundleDeployment{
					ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: name},
				})).ToNot(HaveOccurred())
			})
		})

		Context("Modifying externalName in service resource", func() {
			It("Leaves the bundle deployment untouched", func() {
				By("Receiving a modification on a service")
				svc, err := env.getService("svc-ext")
				Expect(err).NotTo(HaveOccurred())
				patchedSvc := svc.DeepCopy()
				patchedSvc.Spec.ExternalName = "modified"
				Expect(k8sClient.Patch(ctx, patchedSvc, client.StrategicMergeFrom(&svc))).NotTo(HaveOccurred())

				By("Preserving the modification on the service")
				Consistently(func(g Gomega) {
					svc, err := env.getService("svc-ext")
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(svc.Spec.ExternalName).Should(Equal("modified"))
				}, 2*time.Second, 100*time.Millisecond).Should(Succeed())
			})
		})
	})

	When("Drift correction is enabled without force", func() {
		JustBeforeEach(func() {
			correctDrift = v1alpha1.CorrectDrift{Enabled: true}
			env = &specEnv{namespace: namespace}

			createBundleDeployment(name)

			// deployment resources cannot be ready as they rely on being able to pull Docker images
			if name != "drift-deployment-image-test" {
				Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
			}

			DeferCleanup(func() {
				Expect(k8sClient.Delete(context.TODO(), &v1alpha1.BundleDeployment{
					ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: name},
				})).ToNot(HaveOccurred())
			})
		})

		Context("Modifying externalName in a service resource", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-service-externalname-test"
				deplID = "v1"
			})

			It("Corrects drift", func() {
				By("Receiving a modification on a service")
				svc, err := env.getService("svc-ext")
				Expect(err).NotTo(HaveOccurred())
				patchedSvc := svc.DeepCopy()
				patchedSvc.Spec.ExternalName = "modified"
				Expect(k8sClient.Patch(ctx, patchedSvc, client.StrategicMergeFrom(&svc))).NotTo(HaveOccurred())

				By("Restoring the service resource to its previous state")
				Eventually(func(g Gomega) {
					svc, err := env.getService("svc-ext")
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(svc.Spec.ExternalName).Should(Equal("svc-ext"))
				}).Should(Succeed())
			})
		})

		Context("Modifying image in a deployment resource", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-deployment-image-test"
				deplID = "with-deployment"
			})

			It("Corrects drift", func() {
				By("Receiving a modification on a deployment")
				dpl := appsv1.Deployment{}
				nsn := types.NamespacedName{
					Namespace: namespace,
					Name:      "drift-dummy-deployment",
				}

				Eventually(func(g Gomega) {
					bd := &v1alpha1.BundleDeployment{}
					err := k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
					// The bundle deployment will not be ready, because no image can be pulled for
					// the deployment in envtest clusters.
					Expect(err).NotTo(HaveOccurred())

					err = k8sClient.Get(ctx, nsn, &dpl)
					g.Expect(err).ToNot(HaveOccurred())
				}).Should(Succeed())

				patchedDpl := dpl.DeepCopy()
				patchedDpl.Spec.Template.Spec.Containers[0].Image = "foo:modified"
				Expect(k8sClient.Patch(ctx, patchedDpl, client.StrategicMergeFrom(&dpl))).NotTo(HaveOccurred())

				By("Restoring the deployment resource to its previous state")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, nsn, &dpl)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(dpl.Spec.Template.Spec.Containers[0].Image).Should(Equal("k8s.gcr.io/pause"))
				}).Should(Succeed())
			})
		})

		Context("Modifying data in a config map resource", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-configmap-data-test"
				deplID = "v1"
			})

			It("Corrects drift", func() {
				By("Receiving a modification on a config map")
				nsn := types.NamespacedName{
					Namespace: env.namespace,
					Name:      "cm-test",
				}
				cm := corev1.ConfigMap{}
				err := k8sClient.Get(ctx, nsn, &cm)
				Expect(err).ToNot(HaveOccurred())

				patchedCM := cm.DeepCopy()
				patchedCM.Data["foo"] = "modified"
				Expect(k8sClient.Patch(ctx, patchedCM, client.StrategicMergeFrom(&cm))).NotTo(HaveOccurred())

				By("Restoring the config map resource to its previous state")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, nsn, &cm)
					Expect(err).ToNot(HaveOccurred())

					g.Expect(cm.Data["foo"]).Should(Equal("bar"))
				}).Should(Succeed())

				By("Leaving at most 2 Helm history items")
				secrets := corev1.SecretList{}
				err = k8sClient.List(
					ctx,
					&secrets,
					client.MatchingFieldsSelector{
						Selector: fields.SelectorFromSet(map[string]string{"type": "helm.sh/release.v1"}),
					},
					client.InNamespace(env.namespace),
				)
				Expect(err).ToNot(HaveOccurred())
				Expect(len(secrets.Items)).To(BeNumerically("<=", 2), fmt.Sprintf("Expected %#v to contain at most 2 items", secrets.Items))
			})
		})

		// Status must be ignored for drift correction, despite being part of the manifests
		Context("Resource manifests containing status fields", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-status-ignore-test"
				deplID = "with-status"
			})

			It("Marks the bundle deployment as ready", func() {
				Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
			})
		})

		// Helm v4 with client-side apply can successfully handle drift correction on service ports,
		// even when there are complex changes. This is an improvement over Helm v3 where three-way
		// merge would fail on such changes.
		Context("Drift correction succeeds on service port changes", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-test"
				deplID = "v1"
			})

			It("Corrects drift on service port modifications without requiring force", func() {
				By("Receiving a modification on a service")
				svc := corev1.Service{}
				Eventually(func(g Gomega) {
					var err error
					svc, err = env.getService(svcName)
					g.Expect(err).NotTo(HaveOccurred())
				}).Should(Succeed())
				patchedSvc := svc.DeepCopy()
				patchedSvc.Spec.Ports[0].TargetPort = intstr.FromInt(4242)
				patchedSvc.Spec.Ports[0].Port = 4242
				patchedSvc.Spec.Ports[0].Name = "myport"
				Expect(k8sClient.Patch(ctx, patchedSvc, client.StrategicMergeFrom(&svc))).NotTo(HaveOccurred())

				By("Restoring the service resource to its previous state")
				Eventually(func(g Gomega) {
					svc, err := env.getService(svcName)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(svc.Spec.Ports).ToNot(BeEmpty())
					g.Expect(svc.Spec.Ports[0].Port).Should(Equal(int32(80)))
					g.Expect(svc.Spec.Ports[0].TargetPort.IntVal).Should(BeEquivalentTo(9376))
					g.Expect(svc.Spec.Ports[0].Name).Should(Equal("myport"))
				}).Should(Succeed())

				By("Updating the bundle deployment status to be ready and not modified")
				Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
			})
		})
	})

	// Force mode uses resource replacement (DELETE+CREATE) instead of patching (UPDATE).
	// This is needed when:
	// 1. Immutable fields need to be changed (e.g., Service type, PVC storage class, Job selector)
	// 2. Patching fails due to complex conflicts
	// 3. Complete resource recreation is required
	// With Helm v4's improved client-side apply, force mode is rarely needed for common drift scenarios,
	// but it remains available for edge cases that require resource replacement.
	When("Drift correction is enabled with force", func() {
		JustBeforeEach(func() {
			correctDrift = v1alpha1.CorrectDrift{Enabled: true, Force: true}
			env = &specEnv{namespace: namespace}

			createBundleDeployment(name)
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())

			DeferCleanup(func() {
				Expect(k8sClient.Delete(context.TODO(), &v1alpha1.BundleDeployment{
					ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: name},
				})).ToNot(HaveOccurred())
			})
		})

		Context("Modifying a port in a service resource", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-force-service-port-test"
				deplID = "v1"
			})

			It("Corrects drift using resource replacement", func() {
				By("Receiving a modification on a service")
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				patchedSvc := svc.DeepCopy()
				patchedSvc.Spec.Ports[0].TargetPort = intstr.FromInt(4242)
				patchedSvc.Spec.Ports[0].Port = 4242
				patchedSvc.Spec.Ports[0].Name = "myport"
				Expect(k8sClient.Patch(ctx, patchedSvc, client.StrategicMergeFrom(&svc))).NotTo(HaveOccurred())

				By("Restoring the service resource to its previous state")
				// When force=true, Helm uses resource replacement (DELETE+CREATE) instead of patching
				// This should still work for this scenario, demonstrating force mode works correctly
				Eventually(func(g Gomega) {
					svc, err := env.getService(svcName)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(svc.Spec.Ports).ToNot(BeEmpty())
					g.Expect(svc.Spec.Ports[0].Port).Should(Equal(int32(80)))
					g.Expect(svc.Spec.Ports[0].TargetPort.IntVal).Should(BeEquivalentTo(9376))
					g.Expect(svc.Spec.Ports[0].Name).Should(Equal("myport"))
				}).Should(Succeed())

				By("Updating the bundle deployment status to be ready and not modified")
				Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
			})
		})
	})
})
