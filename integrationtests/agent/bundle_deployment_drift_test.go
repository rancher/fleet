package agent

import (
	"context"
	"fmt"
	"os"
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

func init() {
	withStatus, _ := os.ReadFile(assetsPath + "/deployment-with-status.yaml")
	withDeployment, _ := os.ReadFile(assetsPath + "/deployment-with-deployment.yaml")

	resources["with-status"] = []v1alpha1.BundleResource{
		{
			Name:     "deployment-with-status.yaml",
			Content:  string(withStatus),
			Encoding: "",
		},
	}

	resources["with-deployment"] = []v1alpha1.BundleResource{
		{
			Name:     "deployment-with-deployment.yaml",
			Content:  string(withDeployment),
			Encoding: "",
		},
	}
}

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
			It("Receives a modification on a service", func() {
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				patchedSvc := svc.DeepCopy()
				patchedSvc.Spec.ExternalName = "modified"
				Expect(k8sClient.Patch(ctx, patchedSvc, client.StrategicMergeFrom(&svc))).NotTo(HaveOccurred())
			})

			It("Preserves the modification on the service", func() {
				Consistently(func(g Gomega) {
					svc, err := env.getService(svcName)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(svc.Spec.ExternalName).Should(Equal("modified"))
				}, 2*time.Second, 100*time.Millisecond)
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
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				patchedSvc := svc.DeepCopy()
				patchedSvc.Spec.ExternalName = "modified"
				Expect(k8sClient.Patch(ctx, patchedSvc, client.StrategicMergeFrom(&svc))).NotTo(HaveOccurred())

				By("Restoring the service resource to its previous state")
				Eventually(func(g Gomega) {
					svc, err := env.getService(svcName)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(svc.Spec.ExternalName).Should(Equal("svc-test"))
				})
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
					err := k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd, &client.GetOptions{})
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
				})
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
				})

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
				Expect(len(secrets.Items) <= 2).To(BeTrue(), fmt.Sprintf("Expected %#v to contain at most 2 items", secrets.Items))
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

		// Helm rollback uses three-way merge by default (without force), which fails when trying to rollback a
		// change made on an item in the ports array.
		Context("Drift correction fails", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-test"
				deplID = "v1"
			})

			It("Updates the BundleDeployment status as not Ready, including the error message", func() {
				By("Receiving a modification on a service")
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				patchedSvc := svc.DeepCopy()
				patchedSvc.Spec.Ports[0].TargetPort = intstr.FromInt(4242)
				patchedSvc.Spec.Ports[0].Port = 4242
				patchedSvc.Spec.Ports[0].Name = "myport"
				Expect(k8sClient.Patch(ctx, patchedSvc, client.StrategicMergeFrom(&svc))).NotTo(HaveOccurred())

				By("Updating the bundle deployment status")
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

				By("Correcting drift once drift correction is set to force")
				nsn := types.NamespacedName{Namespace: clusterNS, Name: name}
				bd := v1alpha1.BundleDeployment{}

				err = k8sClient.Get(ctx, nsn, &bd, &client.GetOptions{})
				Expect(err).ToNot(HaveOccurred())

				patchedBD := bd.DeepCopy()
				patchedBD.Spec.CorrectDrift.Force = true
				Expect(k8sClient.Patch(ctx, patchedBD, client.MergeFrom(&bd))).NotTo(HaveOccurred())

				By("Restoring the service resource to its previous state")
				Eventually(func(g Gomega) {
					err = k8sClient.Get(ctx, nsn, &bd, &client.GetOptions{})
					g.Expect(err).ToNot(HaveOccurred())

					svc, err := env.getService(svcName)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(svc.Spec.Ports).ToNot(BeEmpty())
					g.Expect(svc.Spec.Ports[0].Port).Should(Equal(80))
					g.Expect(svc.Spec.Ports[0].TargetPort).Should(Equal(9376))
					g.Expect(svc.Spec.Ports[0].Name).Should(Equal("myport"))
				})

				By("Updating the bundle deployment status to be ready and not modified")
				Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
			})
		})
	})

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
			})

			It("Corrects drift", func() {
				By("Receiving a modification on a service")
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
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
					g.Expect(svc.Spec.Ports[0].Port).Should(Equal(80))
					g.Expect(svc.Spec.Ports[0].TargetPort).Should(Equal(9376))
					g.Expect(svc.Spec.Ports[0].Name).Should(Equal("myport"))
				})

				By("Updating the bundle deployment status to be ready and not modified")
				Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
			})
		})
	})
})
