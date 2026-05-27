package bundle

import (
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/experimental"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
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

		// Enable the experimental downstream copy feature.
		Expect(os.Setenv(experimental.CopyResourcesDownstreamFlag, "true")).To(Succeed())

		DeferCleanup(func() {
			os.Unsetenv(experimental.CopyResourcesDownstreamFlag)
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
})
