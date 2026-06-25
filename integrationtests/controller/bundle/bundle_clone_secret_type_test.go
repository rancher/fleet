package bundle

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/experimental"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// On this release branch the downstream-resource copy is still gated by the
// EXPERIMENTAL_COPY_RESOURCES_DOWNSTREAM flag (it was made unconditional
// upstream), so the test enables it explicitly.
var _ = Describe("Bundle cloneSecret preserves immutable Secret type", func() {
	BeforeEach(func() {
		Expect(os.Setenv(experimental.CopyResourcesDownstreamFlag, "true")).To(Succeed())
		DeferCleanup(func() {
			Expect(os.Unsetenv(experimental.CopyResourcesDownstreamFlag)).To(Succeed())
		})

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
				latestbundle := &v1alpha1.Bundle{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bundle), latestbundle)).To(Succeed())
				g.Expect(latestbundle.Status.ObservedGeneration).To(BeNumerically("==", 1))
			}).Should(Succeed())
		})
	})
})

// cleanupBundleDeployments is defined here because that
// addition was not part of this backport.
func cleanupBundleDeployments(bundleName, bundleNamespace string) {
	bdList := &v1alpha1.BundleDeploymentList{}
	Expect(k8sClient.List(ctx, bdList, client.MatchingLabels{
		"fleet.cattle.io/bundle-name":      bundleName,
		"fleet.cattle.io/bundle-namespace": bundleNamespace,
	})).To(Succeed())
	for i := range bdList.Items {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &bdList.Items[i]))).NotTo(HaveOccurred())
	}
}
