package bundle

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Bundle target DownstreamResources", Ordered, func() {
	BeforeAll(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).ToNot(HaveOccurred())

		createClustersAndClusterGroups()

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, ns)).ToNot(HaveOccurred())
		})
	})

	When("a target has DownstreamResources", func() {
		const bundleName = "target-downstream-resources"

		BeforeEach(func() {
			// Create a secret in the bundle namespace to be copied downstream.
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "target-secret",
					Namespace: namespace,
				},
				Data: map[string][]byte{"key": []byte("value")},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, secret))).To(Succeed())
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &v1alpha1.Bundle{
					ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: bundleName},
				}))).NotTo(HaveOccurred())
				cleanupBundleDeployments(bundleName, namespace)
			})
		})

		It("copies the secret to the matching cluster's namespace only", func() {
			By("creating a bundle with per-target DownstreamResources targeting cluster one")
			targets := []v1alpha1.BundleTarget{
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						DownstreamResources: []v1alpha1.DownstreamResource{
							{Kind: "Secret", Name: "target-secret"},
						},
					},
					ClusterGroup: "one",
				},
				{
					ClusterGroup: "all",
				},
			}
			bundle, err := utils.CreateBundle(ctx, k8sClient, bundleName, namespace, targets, targets)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle).NotTo(BeNil())

			By("verifying that a BundleDeployment is created for each matched cluster")
			bdLabels := map[string]string{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": namespace,
			}
			bdList := verifyBundlesDeploymentsAreCreated(3, bdLabels, bundleName)

			By("finding the BundleDeployment for cluster one")
			var clusterOneBD *v1alpha1.BundleDeployment
			for i := range bdList.Items {
				if strings.Contains(bdList.Items[i].Namespace, "cluster-one") {
					clusterOneBD = &bdList.Items[i]
					break
				}
			}
			Expect(clusterOneBD).NotTo(BeNil(), "expected BundleDeployment for cluster one")

			By("verifying DownstreamResources is set in cluster one's BundleDeployment options")
			Eventually(func(g Gomega) {
				bd := &v1alpha1.BundleDeployment{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      clusterOneBD.Name,
					Namespace: clusterOneBD.Namespace,
				}, bd)).To(Succeed())
				g.Expect(bd.Spec.Options.DownstreamResources).To(ConsistOf(
					v1alpha1.DownstreamResource{Kind: "Secret", Name: "target-secret"},
				))
			}).Should(Succeed())

			By("verifying the secret is copied to cluster one's namespace")
			Eventually(func(g Gomega) {
				copied := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      "target-secret",
					Namespace: clusterOneBD.Namespace,
				}, copied)).To(Succeed())
				g.Expect(copied.Data).To(HaveKeyWithValue("key", []byte("value")))
			}).Should(Succeed())

			By("verifying the secret is NOT copied to other clusters' namespaces")
			for _, bd := range bdList.Items {
				if bd.Namespace == clusterOneBD.Namespace {
					continue
				}
				bdNs := bd.Namespace
				Consistently(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      "target-secret",
						Namespace: bdNs,
					}, &corev1.Secret{})
					g.Expect(client.IgnoreNotFound(err)).To(Succeed())
					g.Expect(err).To(HaveOccurred(), "secret should not exist in namespace %s", bdNs)
				}).Should(Succeed())
			}
		})
	})

	When("root-level and per-target DownstreamResources are both specified", func() {
		const bundleName = "mixed-downstream-resources"

		BeforeEach(func() {
			rootSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "root-secret", Namespace: namespace},
				Data:       map[string][]byte{"root": []byte("data")},
			}
			targetSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "target-secret-two", Namespace: namespace},
				Data:       map[string][]byte{"target": []byte("data")},
			}
			Expect(k8sClient.Create(ctx, rootSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, targetSecret)).To(Succeed())
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, rootSecret))).To(Succeed())
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, targetSecret))).To(Succeed())
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &v1alpha1.Bundle{
					ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: bundleName},
				}))).NotTo(HaveOccurred())
				cleanupBundleDeployments(bundleName, namespace)
			})
		})

		It("copies root-level secret to all clusters and target secret only to cluster one", func() {
			By("creating a bundle with root-level and per-target DownstreamResources")
			targets := []v1alpha1.BundleTarget{
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						DownstreamResources: []v1alpha1.DownstreamResource{
							{Kind: "Secret", Name: "target-secret-two"},
						},
					},
					ClusterGroup: "one",
				},
				{
					ClusterGroup: "all",
				},
			}
			bundle := &v1alpha1.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bundleName,
					Namespace: namespace,
				},
				Spec: v1alpha1.BundleSpec{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						DownstreamResources: []v1alpha1.DownstreamResource{
							{Kind: "Secret", Name: "root-secret"},
						},
					},
					Targets: targets,
					TargetRestrictions: func() []v1alpha1.BundleTargetRestriction {
						out := make([]v1alpha1.BundleTargetRestriction, len(targets))
						for i, t := range targets {
							out[i] = v1alpha1.BundleTargetRestriction{
								ClusterGroup: t.ClusterGroup,
							}
						}
						return out
					}(),
				},
			}
			Expect(k8sClient.Create(ctx, bundle)).To(Succeed())

			By("verifying that a BundleDeployment is created for each matched cluster")
			bdLabels := map[string]string{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": namespace,
			}
			bdList := verifyBundlesDeploymentsAreCreated(3, bdLabels, bundleName)

			By("verifying root-secret is copied to all cluster namespaces")
			for _, bd := range bdList.Items {
				bdNs := bd.Namespace
				Eventually(func(g Gomega) {
					copied := &corev1.Secret{}
					g.Expect(k8sClient.Get(ctx, types.NamespacedName{
						Name:      "root-secret",
						Namespace: bdNs,
					}, copied)).To(Succeed())
				}).Should(Succeed())
			}

			By("verifying target-secret-two is copied to cluster one's namespace")
			for _, bd := range bdList.Items {
				if !strings.Contains(bd.Namespace, "cluster-one") {
					continue
				}
				bdNs := bd.Namespace
				Eventually(func(g Gomega) {
					copied := &corev1.Secret{}
					g.Expect(k8sClient.Get(ctx, types.NamespacedName{
						Name:      "target-secret-two",
						Namespace: bdNs,
					}, copied)).To(Succeed())
				}).Should(Succeed())
			}

			By("verifying target-secret-two is NOT copied to other cluster namespaces")
			for _, bd := range bdList.Items {
				if strings.Contains(bd.Namespace, "cluster-one") {
					continue
				}
				bdNs := bd.Namespace
				Consistently(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      "target-secret-two",
						Namespace: bdNs,
					}, &corev1.Secret{})
					g.Expect(client.IgnoreNotFound(err)).To(Succeed())
					g.Expect(err).To(HaveOccurred(), "target-secret-two should not exist in namespace %s", bdNs)
				}).Should(Succeed())
			}
		})
	})

	When("a secret is referenced both as a helm secret and a downstream resource", func() {
		const bundleName = "target-downstream-secret"
		BeforeEach(func() {
			genericSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "mysecret", Namespace: namespace},
				Data:       map[string][]byte{"key": []byte("value")},
			}
			Expect(k8sClient.Create(ctx, genericSecret)).To(Succeed())
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, genericSecret))).To(Succeed())
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &v1alpha1.Bundle{
					ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: bundleName},
				}))).NotTo(HaveOccurred())
				cleanupBundleDeployments(bundleName, namespace)
			})
		})
		It("does not loop forever trying to update the immutable secret type", func() {
			bundle := &v1alpha1.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bundleName,
					Namespace: namespace,
				},
				Spec: v1alpha1.BundleSpec{
					HelmOpOptions: &v1alpha1.BundleHelmOptions{
						SecretName: "mysecret",
					},
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						DownstreamResources: []v1alpha1.DownstreamResource{
							{Kind: "Secret", Name: "mysecret"},
						},
					},
					Targets: []v1alpha1.BundleTarget{
						{ClusterGroup: "one"},
					},
					TargetRestrictions: []v1alpha1.BundleTargetRestriction{
						{ClusterGroup: "one"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, bundle)).To(Succeed())
			Eventually(func(g Gomega) {
				latestBundle := &v1alpha1.Bundle{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bundle), latestBundle)).To(Succeed())
				g.Expect(latestBundle.Status.ObservedGeneration).To(BeNumerically("==", 1))
			}).Should(Succeed())
		})
	})

	When("the data of a cloned downstream secret changes upstream", func() {
		const bundleName = "downstream-secret-update"

		BeforeEach(func() {
			genericSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "have-secret", Namespace: namespace},
				Data:       map[string][]byte{"key": []byte("first")},
			}
			Expect(k8sClient.Create(ctx, genericSecret)).To(Succeed())
			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, genericSecret))).To(Succeed())
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &v1alpha1.Bundle{
					ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: bundleName},
				}))).NotTo(HaveOccurred())
				cleanupBundleDeployments(bundleName, namespace)
			})
		})

		It("propagates the new data to the downstream copy while keeping its type", func() {
			By("creating a bundle that clones the secret downstream")
			bundle := &v1alpha1.Bundle{
				ObjectMeta: metav1.ObjectMeta{Name: bundleName, Namespace: namespace},
				Spec: v1alpha1.BundleSpec{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						DownstreamResources: []v1alpha1.DownstreamResource{
							{Kind: "Secret", Name: "have-secret"},
						},
					},
					Targets: []v1alpha1.BundleTarget{
						{ClusterGroup: "one"},
					},
					TargetRestrictions: []v1alpha1.BundleTargetRestriction{
						{ClusterGroup: "one"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, bundle)).To(Succeed())

			By("finding the cluster namespace of the single BundleDeployment")
			var bdNamespace string
			Eventually(func(g Gomega) {
				bdList := &v1alpha1.BundleDeploymentList{}
				g.Expect(k8sClient.List(ctx, bdList, client.MatchingLabels{
					"fleet.cattle.io/bundle-name":      bundleName,
					"fleet.cattle.io/bundle-namespace": namespace,
				})).To(Succeed())
				g.Expect(bdList.Items).To(HaveLen(1))
				bdNamespace = bdList.Items[0].Namespace
			}).Should(Succeed())

			By("waiting for the clone to appear with the initial data")
			Eventually(func(g Gomega) {
				copied := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: "have-secret", Namespace: bdNamespace,
				}, copied)).To(Succeed())
				g.Expect(copied.Data).To(HaveKeyWithValue("key", []byte("first")))
			}).Should(Succeed())

			By("updating the upstream secret's data")
			Eventually(func(g Gomega) {
				src := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: "have-secret", Namespace: namespace,
				}, src)).To(Succeed())
				src.Data["key"] = []byte("second")
				g.Expect(k8sClient.Update(ctx, src)).To(Succeed())
			}).Should(Succeed())

			// The secret->bundle watch relies on a field indexer that is only
			// registered in the production operator, not in this test suite, so
			// changing the source secret does not re-trigger the bundle. Bump an
			// annotation to force a reconcile (AnnotationChangedPredicate).
			By("triggering a bundle reconcile so the clone is re-synced")
			Eventually(func(g Gomega) {
				latest := &v1alpha1.Bundle{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bundle), latest)).To(Succeed())
				if latest.Annotations == nil {
					latest.Annotations = map[string]string{}
				}
				latest.Annotations["test/trigger"] = "1"
				g.Expect(k8sClient.Update(ctx, latest)).To(Succeed())
			}).Should(Succeed())

			By("verifying the downstream copy reflects the new data and keeps its type")
			Eventually(func(g Gomega) {
				copied := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: "have-secret", Namespace: bdNamespace,
				}, copied)).To(Succeed())
				g.Expect(copied.Data).To(HaveKeyWithValue("key", []byte("second")))
				g.Expect(copied.Type).To(Equal(corev1.SecretTypeOpaque))
			}).Should(Succeed())
		})
	})

	When("a Policy sets requireServiceAccount", func() {
		const (
			bundleName = "policy-downstream-resources"
			secretName = "operator-secret"
			policyName = "tenant-policy"
		)

		var secret *corev1.Secret

		BeforeEach(func() {
			policy := &v1alpha1.Policy{
				ObjectMeta:            metav1.ObjectMeta{Name: policyName, Namespace: namespace},
				RequireServiceAccount: true,
			}
			Expect(k8sClient.Create(ctx, policy)).To(Succeed())

			secret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace},
				Data:       map[string][]byte{"password": []byte("first")},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &v1alpha1.Bundle{
					ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: bundleName},
				}))).NotTo(HaveOccurred())
				// The Bundle has a finalizer, so wait until it is gone before the
				// next spec creates one with the same name.
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: bundleName}, &v1alpha1.Bundle{})
					g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "bundle still exists: %v", err)
				}).Should(Succeed())
				cleanupBundleDeployments(bundleName, namespace)
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, secret))).To(Succeed())
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &v1alpha1.Policy{
					ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: policyName},
				}))).To(Succeed())
			})
		})

		setAllowedDownstreamResources := func(allowed ...v1alpha1.AllowedDownstreamResource) {
			Eventually(func(g Gomega) {
				policy := &v1alpha1.Policy{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: policyName}, policy)).To(Succeed())
				policy.AllowedDownstreamResources = allowed
				g.Expect(k8sClient.Update(ctx, policy)).To(Succeed())
			}).Should(Succeed())
		}

		readyCondition := func(g Gomega) *genericcondition.GenericCondition {
			b := &v1alpha1.Bundle{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: bundleName}, b)).To(Succeed())
			for i := range b.Status.Conditions {
				if b.Status.Conditions[i].Type == string(v1alpha1.Ready) {
					return &b.Status.Conditions[i]
				}
			}
			return nil
		}

		expectRefused := func() {
			Eventually(func(g Gomega) {
				ready := readyCondition(g)
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(corev1.ConditionFalse))
				g.Expect(ready.Message).To(ContainSubstring("downstream resource Secret " + secretName + " is not allowed by Policy"))
			}).Should(Succeed())
		}

		listBDs := func(g Gomega) []v1alpha1.BundleDeployment {
			bdList := &v1alpha1.BundleDeploymentList{}
			g.Expect(k8sClient.List(ctx, bdList, client.MatchingLabels{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": namespace,
			})).To(Succeed())
			return bdList.Items
		}

		stagedCopies := func(g Gomega) []corev1.Secret {
			bds := listBDs(g)
			g.Expect(bds).To(HaveLen(3))
			copies := []corev1.Secret{}
			for _, bd := range bds {
				copied := corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: bd.Namespace, Name: secretName}, &copied)).To(Succeed())
				copies = append(copies, copied)
			}
			return copies
		}

		createBundle := func() {
			bundle := &v1alpha1.Bundle{
				ObjectMeta: metav1.ObjectMeta{Name: bundleName, Namespace: namespace},
				Spec: v1alpha1.BundleSpec{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						ServiceAccount:      "tenant-sa",
						DownstreamResources: []v1alpha1.DownstreamResource{{Kind: "Secret", Name: secretName}},
					},
					Targets: []v1alpha1.BundleTarget{{ClusterGroup: "all"}},
				},
			}
			Expect(k8sClient.Create(ctx, bundle)).To(Succeed())
		}

		It("copies only Secrets the Policy allows, and freezes copies when the Policy stops allowing them", func() {
			By("creating a bundle referencing a secret the Policy does not allow")
			createBundle()

			By("verifying the bundle is refused and nothing is deployed or copied")
			expectRefused()
			Consistently(func(g Gomega) {
				g.Expect(listBDs(g)).To(BeEmpty())
			}, 3*time.Second).Should(Succeed())

			By("verifying an entry for another name or kind does not allow the copy")
			setAllowedDownstreamResources(
				v1alpha1.AllowedDownstreamResource{Kind: "Secret", Name: "other"},
				v1alpha1.AllowedDownstreamResource{Kind: "ConfigMap", Name: secretName},
			)
			expectRefused()
			Consistently(func(g Gomega) {
				g.Expect(listBDs(g)).To(BeEmpty())
			}, 3*time.Second).Should(Succeed())

			By("allowing the secret by pattern in the Policy, which re-triggers the refused bundle")
			setAllowedDownstreamResources(v1alpha1.AllowedDownstreamResource{Kind: "Secret", NamePattern: "operator-.*"})
			Eventually(func(g Gomega) {
				for _, c := range stagedCopies(g) {
					g.Expect(c.Data).To(HaveKeyWithValue("password", []byte("first")))
				}
			}).Should(Succeed())

			By("removing the entry from the Policy and changing the data")
			setAllowedDownstreamResources()
			Eventually(func(g Gomega) {
				src := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(secret), src)).To(Succeed())
				src.Data["password"] = []byte("second")
				g.Expect(k8sClient.Update(ctx, src)).To(Succeed())
			}).Should(Succeed())

			By("verifying the bundle is refused and existing copies are kept but not updated")
			expectRefused()
			Consistently(func(g Gomega) {
				for _, c := range stagedCopies(g) {
					g.Expect(c.Data).To(HaveKeyWithValue("password", []byte("first")))
				}
			}, 3*time.Second).Should(Succeed())

			By("pausing the bundle and dropping the secret from its spec")
			// The Bundle's spec now passes the Policy check, but the paused
			// BundleDeployments still name the secret in their options.
			Eventually(func(g Gomega) {
				b := &v1alpha1.Bundle{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: bundleName}, b)).To(Succeed())
				b.Spec.Paused = true
				b.Spec.DownstreamResources = nil
				g.Expect(k8sClient.Update(ctx, b)).To(Succeed())
			}).Should(Succeed())

			By("verifying the copy step refuses the secret and copies are still not updated")
			Eventually(func(g Gomega) {
				ready := readyCondition(g)
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Message).To(ContainSubstring("failed to clone config maps and secrets downstream"))
				g.Expect(ready.Message).To(ContainSubstring("downstream resource Secret " + secretName + " is not allowed by Policy"))
			}).Should(Succeed())
			Consistently(func(g Gomega) {
				for _, c := range stagedCopies(g) {
					g.Expect(c.Data).To(HaveKeyWithValue("password", []byte("first")))
				}
			}, 3*time.Second).Should(Succeed())
		})

		It("checks the bundle again when a Policy is deleted or created", func() {
			By("creating a bundle referencing a secret the Policy does not allow")
			createBundle()
			expectRefused()

			By("deleting the Policy, which re-triggers the refused bundle")
			Expect(k8sClient.Delete(ctx, &v1alpha1.Policy{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: policyName},
			})).To(Succeed())
			Eventually(func(g Gomega) {
				for _, c := range stagedCopies(g) {
					g.Expect(c.Data).To(HaveKeyWithValue("password", []byte("first")))
				}
				ready := readyCondition(g)
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Message).NotTo(ContainSubstring("is not allowed by Policy"))
			}).Should(Succeed())

			By("creating a Policy again, which refuses the deployed bundle")
			Expect(k8sClient.Create(ctx, &v1alpha1.Policy{
				ObjectMeta:            metav1.ObjectMeta{Name: policyName, Namespace: namespace},
				RequireServiceAccount: true,
			})).To(Succeed())
			expectRefused()
		})
	})

})
