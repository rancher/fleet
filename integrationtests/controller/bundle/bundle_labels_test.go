package bundle

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Bundle labels", func() {
	BeforeEach(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: namespace},
		})).ToNot(HaveOccurred())

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).ToNot(HaveOccurred())
		})
	})

	When("BundleDeployment labels are updated", func() {
		BeforeEach(func() {
			cluster, err := utils.CreateCluster(ctx, k8sClient, "cluster", namespace, nil, namespace)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster).To(Not(BeNil()))
			targets := []v1alpha1.BundleTarget{
				{
					BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
						TargetNamespace: "targetNs",
					},
					Name:        "cluster",
					ClusterName: "cluster",
				},
			}
			bundle, err := utils.CreateBundle(ctx, k8sClient, "name", namespace, targets, targets)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle).To(Not(BeNil()))
		})

		AfterEach(func() {
			Expect(k8sClient.Delete(ctx, &v1alpha1.Bundle{ObjectMeta: metav1.ObjectMeta{
				Name:      "name",
				Namespace: namespace,
			}})).NotTo(HaveOccurred())

		})

		It("Bundle is created", func() {
			bundle := &v1alpha1.Bundle{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "name"}, bundle)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle).To(Not(BeNil()))

			bdLabels := map[string]string{
				"fleet.cattle.io/bundle-name":      bundle.Name,
				"fleet.cattle.io/bundle-namespace": bundle.Namespace,
			}

			By("BundleDeployment has the foo label from Bundle")
			Eventually(func() bool {
				bd, ok := expectedLabelValue(bdLabels, "foo", "bar")
				if !ok {
					return false
				}
				Expect(bd.Labels).To(HaveKeyWithValue("fleet.cattle.io/cluster", "cluster"))
				Expect(bd.Labels).To(HaveKeyWithValue("fleet.cattle.io/cluster-namespace", namespace))
				return true
			}).Should(BeTrue())

			By("Modifying foo label in Bundle")
			labelPatch := `[{"op":"add","path":"/metadata/labels/foo","value":"modified"}]`
			err = k8sClient.Patch(ctx, bundle, client.RawPatch(types.JSONPatchType, []byte(labelPatch)))
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle).To(Not(BeNil()))

			By("Should modify foo label in BundleDeployment")
			Eventually(func() bool {
				_, ok := expectedLabelValue(bdLabels, "foo", "modified")
				return ok
			}).Should(BeTrue())

		})
	})
})
