package agent

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func capabilityBundleResources() map[string][]v1alpha1.BundleResource {
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
				Content: "helm:\n  chart: config-chart\n  values:\n    name: example-value\n",
				Name:    "fleet.yaml",
			},
		},
		"capabilitiesv2": {
			{
				Content: "apiVersion: v2\nname: config-chart\ndescription: A test chart that verifies its config\ntype: application\nversion: 0.1.0\nappVersion: \"1.16.0\"\nkubeVersion: '>= 920.920.0-0'\n",
				Name:    "config-chart/Chart.yaml",
			},
			{
				Content: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test-simple-chart-config\ndata:\n  test: \"value123\"\n  name: {{ .Values.name }}\n",
				Name:    "config-chart/templates/configmap.yaml",
			},
			{
				Content: "helm:\n  chart: config-chart\n  values:\n    name: example-value\n",
				Name:    "fleet.yaml",
			},
		},
	}
}

var _ = Describe("Helm Chart uses Capabilities", Ordered, func() {

	var (
		env *specEnv
	)

	BeforeAll(func() {
		env = specEnvs["capabilitybundle"]
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: env.namespace}})).ToNot(HaveOccurred())
		})
	})

	createBundle := func(env *specEnv, id string, name string) {
		bundled := v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: env.namespace,
			},
			Spec: v1alpha1.BundleDeploymentSpec{
				DeploymentID: id,
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

		b, err := env.controller.Create(&bundled)
		Expect(err).ToNot(HaveOccurred())
		Expect(b).To(Not(BeNil()))
	}

	When("chart kubeversion matches cluster", func() {
		BeforeAll(func() {
			createBundle(env, "capabilitiesv1", "capav1")
			DeferCleanup(func() {
				Expect(env.controller.Delete(env.namespace, "capav1", &metav1.DeleteOptions{})).ToNot(HaveOccurred())
			})
		})

		It("config map from chart is deployed", func() {
			cm := corev1.ConfigMap{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: env.namespace, Name: "test-simple-chart-config"}, &cm)
				return err == nil
			}).Should(BeTrue())
		})
	})

	When("chart kubeversion does not match cluster", func() {
		BeforeAll(func() {
			createBundle(env, "capabilitiesv2", "capav2")
			DeferCleanup(func() {
				Expect(env.controller.Delete(env.namespace, "capav2", &metav1.DeleteOptions{})).ToNot(HaveOccurred())
			})
		})

		It("error message is added to status", func() {
			Eventually(func() bool {
				bd, err := env.controller.Get(env.namespace, "capav2", metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				return checkCondition(bd.Status.Conditions, "Ready", "False", "chart requires kubeVersion")
			}).Should(BeTrue())
		})
	})
})
