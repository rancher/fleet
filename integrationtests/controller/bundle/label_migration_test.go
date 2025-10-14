package bundle

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Bundle label migration", func() {
	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: namespace},
		})).ToNot(HaveOccurred())

		_, err = utils.CreateCluster(ctx, k8sClient, "cluster", namespace, nil, namespace)
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).ToNot(HaveOccurred())
		})
	})

	createBundle := func(name string, labels map[string]string) {
		bundle := &v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
			},
			Spec: v1alpha1.BundleSpec{
				BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
					DefaultNamespace: "default",
				},
				Targets: []v1alpha1.BundleTarget{
					{
						BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
							TargetNamespace: "targetNs",
						},
						Name:        "cluster",
						ClusterName: "cluster",
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, bundle)).ToNot(HaveOccurred())
	}

	DescribeTable("should remove deprecated label after migration",
		func(bundleName string, initialLabels map[string]string, shouldHaveDisplayLabel bool) {
			const deprecatedLabel = "fleet.cattle.io/created-by-display-name"

			createBundle(bundleName, initialLabels)
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, &v1alpha1.Bundle{
					ObjectMeta: metav1.ObjectMeta{Name: bundleName, Namespace: namespace},
				})).ToNot(HaveOccurred())
			})

			Eventually(func(g Gomega) {
				bundle := &v1alpha1.Bundle{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: bundleName}, bundle)).To(Succeed())
				g.Expect(bundle.Status.ObservedGeneration).To(BeNumerically(">", 0))
			}).Should(Succeed())

			bundle := &v1alpha1.Bundle{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: bundleName}, bundle)).To(Succeed())

			Expect(bundle.Labels).ToNot(HaveKey(deprecatedLabel))
			Expect(bundle.Labels).To(HaveKey(v1alpha1.CreatedByUserIDLabel))
		},
		Entry("with label present initially",
			"bundle-with-label",
			map[string]string{
				"fleet.cattle.io/created-by-display-name": "admin",
				v1alpha1.CreatedByUserIDLabel:             "user-12345",
			},
			true,
		),
		Entry("without label present initially",
			"bundle-without-label",
			map[string]string{
				v1alpha1.CreatedByUserIDLabel: "user-12345",
			},
			false,
		),
	)
})
