package agent

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	v1gen "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Bundle labels", func() {

	var (
		env                        *specEnv
		bundleController           v1gen.BundleController
		clusterController          v1gen.ClusterController
		bundleDeploymentController v1gen.BundleDeploymentController
	)

	BeforeEach(func() {
		env = specEnvs["labels"]
		bundleController = env.fleet.V1alpha1().Bundle()
		clusterController = env.fleet.V1alpha1().Cluster()
		bundleDeploymentController = env.fleet.V1alpha1().BundleDeployment()

		DeferCleanup(func() {
			Expect(env.k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: env.namespace}})).ToNot(HaveOccurred())
		})
	})

	When("BundleDeployment labels are updated", func() {
		BeforeEach(func() {
			cluster, err := createCluster("cluster", env.namespace, clusterController, nil, env.namespace)
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
			bundle, err := createBundle("name", env.namespace, bundleController, targets, targets)
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle).To(Not(BeNil()))
		})

		AfterEach(func() {
			Expect(bundleController.Delete(env.namespace, "name", nil)).NotTo(HaveOccurred())
		})

		It("Bundle is created", func() {
			bundle, err := bundleController.Get(env.namespace, "name", metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle).To(Not(BeNil()))

			bdLabels := map[string]string{
				"fleet.cattle.io/bundle-name":      bundle.Name,
				"fleet.cattle.io/bundle-namespace": bundle.Namespace,
			}

			By("BundleDeployment has the foo label from Bundle")
			Eventually(func() bool {
				return expectedLabelValue(bundleDeploymentController, bdLabels, "foo", "bar")
			}).Should(BeTrue())

			By("Modifying foo label in Bundle")
			labelPatch := `[{"op":"add","path":"/metadata/labels/foo","value":"modified"}]`
			bundle, err = bundleController.Patch(bundle.ObjectMeta.Namespace, bundle.Name, types.JSONPatchType, []byte(labelPatch))
			Expect(err).NotTo(HaveOccurred())
			Expect(bundle).To(Not(BeNil()))

			By("Should modify foo label in BundleDeployment")
			Eventually(func() bool {
				return expectedLabelValue(bundleDeploymentController, bdLabels, "foo", "modified")
			}).Should(BeTrue())

		})
	})
})

func expectedLabelValue(controller v1gen.BundleDeploymentController, bdLabels map[string]string, key, value string) bool {
	list, err := controller.List("", metav1.ListOptions{LabelSelector: labels.SelectorFromSet(bdLabels).String()})
	Expect(err).NotTo(HaveOccurred())
	if len(list.Items) == 1 {
		return list.Items[0].Labels[key] == value
	}
	return false
}
