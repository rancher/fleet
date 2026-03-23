package agent_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
			if deplID != "with-deployment" {
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

		Context("Modifying a label on a deployment resource", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-deployment-label-test"
				deplID = "with-deployment"
			})

			It("Corrects drift", func() {
				By("Modifying an existing label on a deployment")
				dpl := appsv1.Deployment{}
				nsn := types.NamespacedName{
					Namespace: namespace,
					Name:      "drift-dummy-deployment",
				}

				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, nsn, &dpl)
					g.Expect(err).ToNot(HaveOccurred())
				}).Should(Succeed())

				patchedDpl := dpl.DeepCopy()
				patchedDpl.Labels["app"] = "modified"
				Expect(k8sClient.Patch(ctx, patchedDpl, client.MergeFrom(&dpl))).NotTo(HaveOccurred())

				By("Restoring the deployment label to its previous state")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, nsn, &dpl)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(dpl.Labels["app"]).Should(Equal("drift-dummy"))
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
					g.Expect(err).ToNot(HaveOccurred())

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

		Context("Modifying replicas in a deployment resource", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-deployment-replicas-test"
				deplID = "with-deployment"
			})

			It("Corrects drift", func() {
				By("Receiving a modification on a deployment's replicas")
				dpl := appsv1.Deployment{}
				nsn := types.NamespacedName{
					Namespace: namespace,
					Name:      "drift-dummy-deployment",
				}

				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, nsn, &dpl)
					g.Expect(err).ToNot(HaveOccurred())
				}).Should(Succeed())

				patchedDpl := dpl.DeepCopy()
				replicas := int32(5)
				patchedDpl.Spec.Replicas = &replicas
				Expect(k8sClient.Patch(ctx, patchedDpl, client.StrategicMergeFrom(&dpl))).NotTo(HaveOccurred())

				By("Restoring the deployment replicas to its previous state")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, nsn, &dpl)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(dpl.Spec.Replicas).ToNot(BeNil())
					g.Expect(*dpl.Spec.Replicas).Should(Equal(int32(1)))
				}).Should(Succeed())
			})
		})

		Context("Externally deleted ConfigMap resource", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-deleted-configmap-test"
				deplID = "v1"
			})

			It("Recreates the deleted resource", func() {
				By("Deleting a managed ConfigMap")
				nsn := types.NamespacedName{
					Namespace: namespace,
					Name:      "cm-test",
				}
				cm := corev1.ConfigMap{}
				err := k8sClient.Get(ctx, nsn, &cm)
				Expect(err).ToNot(HaveOccurred())

				Expect(k8sClient.Delete(ctx, &cm)).ToNot(HaveOccurred())

				By("Verifying the ConfigMap no longer exists")
				err = k8sClient.Get(ctx, nsn, &cm)
				Expect(apierrors.IsNotFound(err)).To(BeTrue())

				By("Waiting for drift correction to recreate the ConfigMap")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, nsn, &cm)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(cm.Data["foo"]).Should(Equal("bar"))
				}).Should(Succeed())

				By("Updating the bundle deployment status to be ready and not modified")
				Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
			})
		})

		Context("Externally deleted Service resource", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-deleted-service-test"
				deplID = "v1"
			})

			It("Recreates the deleted resource", func() {
				By("Deleting a managed Service")
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())

				Expect(k8sClient.Delete(ctx, &svc)).ToNot(HaveOccurred())

				By("Verifying the Service no longer exists")
				nsn := types.NamespacedName{
					Namespace: namespace,
					Name:      svcName,
				}
				err = k8sClient.Get(ctx, nsn, &corev1.Service{})
				Expect(apierrors.IsNotFound(err)).To(BeTrue())

				By("Waiting for drift correction to recreate the Service")
				Eventually(func(g Gomega) {
					svc, err := env.getService(svcName)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(svc.Spec.Ports).ToNot(BeEmpty())
					g.Expect(svc.Spec.Ports[0].Port).Should(Equal(int32(80)))
				}).Should(Succeed())

				By("Updating the bundle deployment status to be ready and not modified")
				Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
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

		// Changing an immutable field like Service spec.type (ClusterIP -> NodePort)
		// cannot be patched — Kubernetes rejects the update. Non-force drift
		// correction cannot fix this; force: true (DELETE+CREATE) is required.
		Context("Changing immutable service type without force", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-immutable-svctype-test"
				deplID = "v1"
			})

			It("Cannot correct immutable field drift without force", func() {
				By("Changing the service type from ClusterIP to NodePort")
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				patchedSvc := svc.DeepCopy()
				patchedSvc.Spec.Type = corev1.ServiceTypeNodePort
				Expect(k8sClient.Patch(ctx, patchedSvc, client.StrategicMergeFrom(&svc))).NotTo(HaveOccurred())

				By("Verifying the drift is not corrected")
				Consistently(func(g Gomega) {
					svc, err := env.getService(svcName)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(svc.Spec.Type).Should(Equal(corev1.ServiceTypeNodePort))
				}, 2*time.Second, 100*time.Millisecond).Should(Succeed())
			})
		})

		// Changing the port number in a Service creates a new entry in the
		// strategic merge patch (port is the merge key), resulting in two
		// ports with the same name — which Kubernetes rejects. Non-force
		// drift correction cannot fix this; force: true (DELETE+CREATE) is
		// required.
		Context("Drift correction fails on service port changes without force", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-test"
				deplID = "v1"
			})

			It("Cannot correct service port drift without force", func() {
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

				By("Verifying the drift is not corrected")
				Consistently(func(g Gomega) {
					svc, err := env.getService(svcName)
					g.Expect(err).NotTo(HaveOccurred())

					g.Expect(svc.Spec.Ports).ToNot(BeEmpty())
					g.Expect(svc.Spec.Ports[0].Port).Should(Equal(int32(4242)))
				}, 2*time.Second, 100*time.Millisecond).Should(Succeed())
			})
		})
	})

	// Force mode uses resource replacement (DELETE+CREATE) instead of patching (UPDATE).
	// This is needed when:
	// 1. Immutable fields need to be changed (e.g., Service type, PVC storage class, Job selector)
	// 2. Strategic merge patch fails due to merge key conflicts (e.g., changing a Service port number)
	// 3. Complete resource recreation is required
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

		Context("Externally deleted ConfigMap resource with force", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-force-deleted-configmap-test"
				deplID = "v1"
			})

			It("Recreates the deleted resource", func() {
				By("Deleting a managed ConfigMap")
				nsn := types.NamespacedName{
					Namespace: namespace,
					Name:      "cm-test",
				}
				cm := corev1.ConfigMap{}
				err := k8sClient.Get(ctx, nsn, &cm)
				Expect(err).ToNot(HaveOccurred())

				Expect(k8sClient.Delete(ctx, &cm)).ToNot(HaveOccurred())

				By("Verifying the ConfigMap no longer exists")
				err = k8sClient.Get(ctx, nsn, &cm)
				Expect(apierrors.IsNotFound(err)).To(BeTrue())

				By("Waiting for drift correction to recreate the ConfigMap")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, nsn, &cm)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(cm.Data["foo"]).Should(Equal("bar"))
				}).Should(Succeed())

				By("Updating the bundle deployment status to be ready and not modified")
				Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
			})
		})

		// Changing an immutable field like Service spec.type requires DELETE+CREATE
		// (force mode), since Kubernetes rejects patching immutable fields.
		Context("Changing immutable service type with force", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-force-immutable-svctype-test"
				deplID = "v1"
			})

			It("Corrects immutable field drift using resource replacement", func() {
				By("Changing the service type from ClusterIP to NodePort")
				svc, err := env.getService(svcName)
				Expect(err).NotTo(HaveOccurred())
				patchedSvc := svc.DeepCopy()
				patchedSvc.Spec.Type = corev1.ServiceTypeNodePort
				Expect(k8sClient.Patch(ctx, patchedSvc, client.StrategicMergeFrom(&svc))).NotTo(HaveOccurred())

				By("Restoring the service type to ClusterIP")
				Eventually(func(g Gomega) {
					svc, err := env.getService(svcName)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(svc.Spec.Type).Should(Equal(corev1.ServiceTypeClusterIP))
				}).Should(Succeed())

				By("Updating the bundle deployment status to be ready and not modified")
				Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
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

	// KeepFailHistory controls whether failed rollback entries are retained
	// in the Helm release history. By default (keepFailHistory=false),
	// removeFailedRollback() deletes the failed entry and restores the
	// previous release as current. With keepFailHistory=true, the failed
	// entry stays in history for debugging.
	When("Drift correction is enabled with keepFailHistory", func() {
		JustBeforeEach(func() {
			correctDrift = v1alpha1.CorrectDrift{Enabled: true, KeepFailHistory: true}
			env = &specEnv{namespace: namespace}

			createBundleDeployment(name)
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())

			DeferCleanup(func() {
				Expect(k8sClient.Delete(context.TODO(), &v1alpha1.BundleDeployment{
					ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: name},
				})).ToNot(HaveOccurred())
			})
		})

		// To trigger a real rollback failure, we modify a ConfigMap's data
		// and then set immutable: true. The drift controller detects the data
		// change and attempts a rollback, but Kubernetes rejects the update
		// because the ConfigMap is immutable. With keepFailHistory=true, the
		// failed rollback release entry is retained in the Helm history.
		Context("Failed rollback retains history entry", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-keep-fail-history-test"
				deplID = "v1"
			})

			It("Keeps the failed rollback entry in Helm history", func() {
				By("Modifying ConfigMap data and making it immutable")
				nsn := types.NamespacedName{
					Namespace: namespace,
					Name:      "cm-test",
				}
				cm := corev1.ConfigMap{}
				err := k8sClient.Get(ctx, nsn, &cm)
				Expect(err).ToNot(HaveOccurred())

				// Change the data and make the ConfigMap immutable in a single
				// patch to avoid a race where the drift controller could
				// fix the data change before immutable is set.
				patchedCM := cm.DeepCopy()
				patchedCM.Data["foo"] = "modified"
				immutable := true
				patchedCM.Immutable = &immutable
				Expect(k8sClient.Patch(ctx, patchedCM, client.MergeFrom(&cm))).NotTo(HaveOccurred())

				By("Waiting for the failed rollback entry to appear in Helm history")
				Eventually(func(g Gomega) {
					secrets := corev1.SecretList{}
					err := k8sClient.List(
						ctx,
						&secrets,
						client.MatchingFieldsSelector{
							Selector: fields.SelectorFromSet(map[string]string{"type": "helm.sh/release.v1"}),
						},
						client.InNamespace(env.namespace),
					)
					g.Expect(err).ToNot(HaveOccurred())

					var hasFailed bool
					for _, s := range secrets.Items {
						if s.Labels["status"] == "failed" {
							hasFailed = true
							break
						}
					}
					g.Expect(hasFailed).To(BeTrue(), "expected a failed rollback entry in Helm history")
				}).Should(Succeed())

				By("Verifying the ConfigMap data was not reverted (rollback failed)")
				cm = corev1.ConfigMap{}
				err = k8sClient.Get(ctx, nsn, &cm)
				Expect(err).ToNot(HaveOccurred())
				Expect(cm.Data["foo"]).To(Equal("modified"))
			})
		})

	})

	// Multiple resources can drift at the same time. A single Helm rollback
	// should restore all of them in one cycle.
	When("Multiple resources drift simultaneously", func() {
		JustBeforeEach(func() {
			correctDrift = v1alpha1.CorrectDrift{Enabled: true}
			env = &specEnv{namespace: namespace}

			createBundleDeployment(name)
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())

			DeferCleanup(func() {
				Expect(k8sClient.Delete(context.TODO(), &v1alpha1.BundleDeployment{
					ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: name},
				})).ToNot(HaveOccurred())
			})
		})

		Context("Modifying ConfigMap data and Service externalName at the same time", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-multi-resource-test"
				deplID = "v1"
			})

			It("Corrects all drifts", func() {
				By("Modifying ConfigMap data")
				cmNSN := types.NamespacedName{Namespace: namespace, Name: "cm-test"}
				cm := corev1.ConfigMap{}
				err := k8sClient.Get(ctx, cmNSN, &cm)
				Expect(err).ToNot(HaveOccurred())
				patchedCM := cm.DeepCopy()
				patchedCM.Data["foo"] = "modified"
				Expect(k8sClient.Patch(ctx, patchedCM, client.StrategicMergeFrom(&cm))).NotTo(HaveOccurred())

				By("Modifying Service externalName")
				svc, err := env.getService("svc-ext")
				Expect(err).NotTo(HaveOccurred())
				patchedSvc := svc.DeepCopy()
				patchedSvc.Spec.ExternalName = "modified"
				Expect(k8sClient.Patch(ctx, patchedSvc, client.StrategicMergeFrom(&svc))).NotTo(HaveOccurred())

				By("Waiting for both resources to be restored")
				Eventually(func(g Gomega) {
					cm := corev1.ConfigMap{}
					err := k8sClient.Get(ctx, cmNSN, &cm)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(cm.Data["foo"]).Should(Equal("bar"))

					svc, err := env.getService("svc-ext")
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(svc.Spec.ExternalName).Should(Equal("svc-ext"))
				}).Should(Succeed())

				By("Verifying the bundle deployment is ready and not modified")
				Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())
			})
		})
	})

	// The drift controller skips correction when the BundleDeployment is
	// paused or outside its schedule window.
	When("Drift correction is paused or off-schedule", func() {
		JustBeforeEach(func() {
			correctDrift = v1alpha1.CorrectDrift{Enabled: true}
			env = &specEnv{namespace: namespace}

			createBundleDeployment(name)
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())

			DeferCleanup(func() {
				Expect(k8sClient.Delete(context.TODO(), &v1alpha1.BundleDeployment{
					ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: name},
				})).ToNot(HaveOccurred())
			})
		})

		Context("BundleDeployment is paused", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-paused-test"
				deplID = "v1"
			})

			It("Does not correct drift while paused", func() {
				By("Pausing the BundleDeployment")
				bd := &v1alpha1.BundleDeployment{}
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
				Expect(err).ToNot(HaveOccurred())
				bd.Spec.Paused = true
				Expect(k8sClient.Update(ctx, bd)).ToNot(HaveOccurred())

				By("Modifying ConfigMap data")
				nsn := types.NamespacedName{Namespace: namespace, Name: "cm-test"}
				cm := corev1.ConfigMap{}
				err = k8sClient.Get(ctx, nsn, &cm)
				Expect(err).ToNot(HaveOccurred())
				patchedCM := cm.DeepCopy()
				patchedCM.Data["foo"] = "modified"
				Expect(k8sClient.Patch(ctx, patchedCM, client.StrategicMergeFrom(&cm))).NotTo(HaveOccurred())

				By("Verifying the drift is not corrected while paused")
				Consistently(func(g Gomega) {
					cm := corev1.ConfigMap{}
					err := k8sClient.Get(ctx, nsn, &cm)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(cm.Data["foo"]).Should(Equal("modified"))
				}, 3*time.Second, 100*time.Millisecond).Should(Succeed())

				By("Unpausing the BundleDeployment")
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
				Expect(err).ToNot(HaveOccurred())
				bd.Spec.Paused = false
				Expect(k8sClient.Update(ctx, bd)).ToNot(HaveOccurred())

				By("Verifying drift is corrected after unpausing")
				Eventually(func(g Gomega) {
					cm := corev1.ConfigMap{}
					err := k8sClient.Get(ctx, nsn, &cm)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(cm.Data["foo"]).Should(Equal("bar"))
				}).Should(Succeed())
			})
		})

		Context("BundleDeployment is off-schedule", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-offschedule-test"
				deplID = "v1"
			})

			It("Does not correct drift while off-schedule", func() {
				By("Setting the BundleDeployment to off-schedule")
				bd := &v1alpha1.BundleDeployment{}
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
				Expect(err).ToNot(HaveOccurred())
				bd.Spec.OffSchedule = true
				Expect(k8sClient.Update(ctx, bd)).ToNot(HaveOccurred())

				By("Modifying Service externalName")
				svc, err := env.getService("svc-ext")
				Expect(err).NotTo(HaveOccurred())
				patchedSvc := svc.DeepCopy()
				patchedSvc.Spec.ExternalName = "modified"
				Expect(k8sClient.Patch(ctx, patchedSvc, client.StrategicMergeFrom(&svc))).NotTo(HaveOccurred())

				By("Verifying the drift is not corrected while off-schedule")
				Consistently(func(g Gomega) {
					svc, err := env.getService("svc-ext")
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(svc.Spec.ExternalName).Should(Equal("modified"))
				}, 3*time.Second, 100*time.Millisecond).Should(Succeed())

				By("Setting the BundleDeployment back on-schedule")
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
				Expect(err).ToNot(HaveOccurred())
				bd.Spec.OffSchedule = false
				Expect(k8sClient.Update(ctx, bd)).ToNot(HaveOccurred())

				By("Verifying drift is corrected after returning to schedule")
				Eventually(func(g Gomega) {
					svc, err := env.getService("svc-ext")
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(svc.Spec.ExternalName).Should(Equal("svc-ext"))
				}).Should(Succeed())
			})
		})
	})

	// Helm's drift detection only tracks fields present in the original
	// chart manifest. Adding a new label that wasn't in the manifest is
	// invisible to drift detection. This is a Helm limitation, not a Fleet
	// issue.
	When("Known limitation: new labels are not detected as drift", func() {
		JustBeforeEach(func() {
			correctDrift = v1alpha1.CorrectDrift{Enabled: true}
			env = &specEnv{namespace: namespace}

			createBundleDeployment(name)
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())

			DeferCleanup(func() {
				Expect(k8sClient.Delete(context.TODO(), &v1alpha1.BundleDeployment{
					ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: name},
				})).ToNot(HaveOccurred())
			})
		})

		Context("Adding a label not present in the manifest", func() {
			BeforeEach(func() {
				namespace = createNamespace()
				name = "drift-new-label-not-detected-test"
				deplID = "v1"
			})

			It("Does not detect the added label as drift", func() {
				By("Adding a new label to a managed ConfigMap")
				nsn := types.NamespacedName{Namespace: namespace, Name: "cm-test"}
				cm := corev1.ConfigMap{}
				err := k8sClient.Get(ctx, nsn, &cm)
				Expect(err).ToNot(HaveOccurred())

				patchedCM := cm.DeepCopy()
				if patchedCM.Labels == nil {
					patchedCM.Labels = map[string]string{}
				}
				patchedCM.Labels["custom-label"] = "added-externally"
				Expect(k8sClient.Patch(ctx, patchedCM, client.MergeFrom(&cm))).NotTo(HaveOccurred())

				By("Verifying the label persists (not removed by drift correction)")
				Consistently(func(g Gomega) {
					cm := corev1.ConfigMap{}
					err := k8sClient.Get(ctx, nsn, &cm)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(cm.Labels).To(HaveKeyWithValue("custom-label", "added-externally"))
				}, 3*time.Second, 100*time.Millisecond).Should(Succeed())

				By("Verifying the bundle deployment remains ready and not modified")
				Expect(env.isBundleDeploymentReadyAndNotModified(name)).To(BeTrue())
			})
		})
	})
})
