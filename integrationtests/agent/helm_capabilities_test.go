package agent

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetgen "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Helm Chart uses Capabilities", Ordered, func() {

	const bundle = "capabilitybundle"

	var (
		controller fleetgen.BundleDeploymentController
		env        specEnv
		namespace  string
	)

	createResources := func() map[string][]v1alpha1.BundleResource {
		return map[string][]v1alpha1.BundleResource{
			"capabilitiesv1": {
				{
					Content: "apiVersion: v2\nname: config-chart\ndescription: A test chart that verifies its config\ntype: application\nversion: 0.1.0\nappVersion: \"1.16.0\"\nkubeVersion: '>= 1.20.0-0'\n",
					Name:    "config-chart/Chart.yaml",
				},
				{
					Content: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test-simple-chart-config\ndata:\n  test: \"value123\"\n  name: {{ .Values.name }}\n",
					Name:    "config-chart/templates/configmap.yaml",
				},
				{
					Content: "name: default-name\n",
					Name:    "config-chart/values.yaml",
				},
				{
					Content: "helm:\n  chart: config-chart\n  values:\n    name: example-value\n",
					Name:    "fleet.yaml",
				},
				{
					Content: "url: global.fleet.clusterLabels.name\n",
					Name:    "values.yaml",
				},
			},
		}
	}

	BeforeAll(func() {
		namespace = newNamespaceName()
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		})).NotTo(HaveOccurred())

		controller = registerBundleDeploymentController(cfg, namespace, newLookup((createResources())))

		env = specEnv{controller: controller, k8sClient: k8sClient, namespace: namespace, name: bundle}

		DeferCleanup(func() {
			Expect(controller.Delete(namespace, bundle, nil)).NotTo(HaveOccurred())
		})
	})

	When("bundle deployment is created", func() {
		BeforeAll(func() {
			bundled := v1alpha1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      bundle,
					Namespace: namespace,
				},
				Spec: v1alpha1.BundleDeploymentSpec{
					DeploymentID: "capabilitiesv1",
					Options: v1alpha1.BundleDeploymentOptions{
						Helm: &v1alpha1.HelmOptions{
							Chart: "config-chart",
							Values: &v1alpha1.GenericMap{
								Data: map[string]interface{}{"name": "example-value"},
							},
						},
					},
				},
			}

			b, err := controller.Create(&bundled)
			Expect(err).ToNot(HaveOccurred())
			Expect(b).To(Not(BeNil()))
			Eventually(env.isBundleDeploymentReadyAndNotModified).Should(BeTrue())
		})

		It("fails to install the helm chart", func() {
			cm := corev1.ConfigMap{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: "test-simple-chart-config"}, &cm)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
