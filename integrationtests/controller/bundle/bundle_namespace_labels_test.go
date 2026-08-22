package bundle

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Bundle namespace labels", func() {
	var bundleName string

	// createBundle creates a bundle targeting the single cluster of the test
	// namespace, with pod-security namespaceLabels and an attempt to opt into
	// applying them from the bundle itself.
	createBundle := func() {
		allow := true
		bundle := &v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{Name: bundleName, Namespace: namespace},
			Spec: v1alpha1.BundleSpec{
				BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
					AllowPodSecurityNamespaceLabels: &allow,
					NamespaceLabels: map[string]string{
						"pod-security.kubernetes.io/enforce": "privileged",
					},
				},
				Targets: []v1alpha1.BundleTarget{{Name: "cluster", ClusterName: "cluster"}},
			},
		}
		Expect(k8sClient.Create(ctx, bundle)).ToNot(HaveOccurred())

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, bundle)).ToNot(HaveOccurred())
			cleanupBundleDeployments(bundleName, namespace)
		})
	}

	bundleDeploymentOptions := func(g Gomega) v1alpha1.BundleDeploymentOptions {
		list := &v1alpha1.BundleDeploymentList{}
		g.Expect(k8sClient.List(ctx, list, client.MatchingLabels{
			"fleet.cattle.io/bundle-name":      bundleName,
			"fleet.cattle.io/bundle-namespace": namespace,
		})).To(Succeed())
		g.Expect(list.Items).To(HaveLen(1))
		return list.Items[0].Spec.Options
	}

	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: namespace},
		})).ToNot(HaveOccurred())

		bundleName = "namespace-labels"

		_, err = utils.CreateCluster(ctx, k8sClient, "cluster", namespace, nil, namespace)
		Expect(err).ToNot(HaveOccurred())

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: namespace},
			})).ToNot(HaveOccurred())
		})
	})

	When("no Policy allows pod-security namespace labels", func() {
		It("does not opt the BundleDeployment into applying them", func() {
			createBundle()

			Eventually(func(g Gomega) {
				opts := bundleDeploymentOptions(g)
				g.Expect(opts.NamespaceLabels).To(
					HaveKeyWithValue("pod-security.kubernetes.io/enforce", "privileged"))
				g.Expect(opts.AllowPodSecurityNamespaceLabels).To(BeNil())
			}).Should(Succeed())
		})
	})

	When("a Policy allows pod-security namespace labels", func() {
		It("opts the BundleDeployment into applying them", func() {
			Expect(k8sClient.Create(ctx, &v1alpha1.Policy{
				ObjectMeta:                      metav1.ObjectMeta{Name: "policy", Namespace: namespace},
				AllowPodSecurityNamespaceLabels: true,
			})).ToNot(HaveOccurred())

			createBundle()

			Eventually(func(g Gomega) {
				opts := bundleDeploymentOptions(g)
				g.Expect(opts.AllowPodSecurityNamespaceLabels).ToNot(BeNil())
				g.Expect(*opts.AllowPodSecurityNamespaceLabels).To(BeTrue())
			}).Should(Succeed())
		})
	})
})
