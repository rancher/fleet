package agent

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func init() {
	resources["capabilitiesv1"] = []v1alpha1.BundleResource{
		{
			Content: "apiVersion: v2\nname: config-chart\ndescription: A test chart that verifies its config\ntype: application\nversion: 0.1.0\nappVersion: \"1.16.0\"\nkubeVersion: '>= 1.20.0-0'\n",
			Name:    "config-chart/Chart.yaml",
		},
		{
			Content: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test-simple-chart-config\ndata:\n  test: \"value123\"\n  name: {{ .Values.name }}\n  kubeVersion: {{ .Capabilities.KubeVersion.Version }}\n  apiVersions: {{ join \", \" .Capabilities.APIVersions |  }}\n  helmVersion: {{ .Capabilities.HelmVersion.Version }}\n",
			Name:    "config-chart/templates/configmap.yaml",
		},
		{
			Content: "helm:\n  chart: config-chart\n  values:\n    name: example-value\n",
			Name:    "fleet.yaml",
		},
	}

	resources["capabilitiesv2"] = []v1alpha1.BundleResource{
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
	}
}

var _ = Describe("Helm Chart uses Capabilities", Ordered, func() {

	var (
		env  *specEnv
		name string
	)

	BeforeAll(func() {
		var err error
		namespace, err := utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		env = &specEnv{namespace: namespace}

		Expect(k8sClient.Create(context.Background(),
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: env.namespace}})).ToNot(HaveOccurred())
		})
	})

	createBundle := func(env *specEnv, id string, name string) {
		bundled := v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: clusterNS,
			},
			Spec: v1alpha1.BundleDeploymentSpec{
				DeploymentID: id,
				Options: v1alpha1.BundleDeploymentOptions{
					DefaultNamespace: env.namespace,
					Helm: &v1alpha1.HelmOptions{
						Chart: "config-chart",
						Values: &v1alpha1.GenericMap{
							Data: map[string]interface{}{"name": "example-value"},
						},
					},
				},
			},
		}

		err := k8sClient.Create(ctx, &bundled)
		Expect(err).ToNot(HaveOccurred())
		Expect(bundled).To(Not(BeNil()))
	}

	When("chart kubeversion matches cluster", func() {
		BeforeAll(func() {
			name = "capav1"
			createBundle(env, "capabilitiesv1", name)
			DeferCleanup(func() {
				Expect(k8sClient.Delete(context.TODO(), &v1alpha1.BundleDeployment{
					ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: name},
				})).ToNot(HaveOccurred())
			})
		})

		It("config map from chart is deployed with capabilities", func() {
			cm := corev1.ConfigMap{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: env.namespace, Name: "test-simple-chart-config"}, &cm)
				return err == nil
			}).Should(BeTrue(), "config map was not created")

			Expect(cm.Data["name"]).To(Equal("example-value"))
			Expect(cm.Data["kubeVersion"]).ToNot(BeEmpty())
			Expect(cm.Data["apiVersions"]).ToNot(BeEmpty())
			Expect(cm.Data["apiVersions"]).To(ContainSubstring("apps/v1"))
			Expect(cm.Data["helmVersion"]).ToNot(BeEmpty())
		})
	})

	When("chart kubeversion does not match cluster", func() {
		BeforeAll(func() {
			name = "capav2"
			createBundle(env, "capabilitiesv2", name)
			DeferCleanup(func() {
				Expect(k8sClient.Delete(context.TODO(), &v1alpha1.BundleDeployment{
					ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: name},
				})).ToNot(HaveOccurred())
			})
		})

		It("error message is added to status", func() {
			Eventually(func() bool {
				bd := &v1alpha1.BundleDeployment{}
				err := k8sClient.Get(context.TODO(), types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
				Expect(err).ToNot(HaveOccurred())
				return checkCondition(bd.Status.Conditions, "Ready", "False", "chart requires kubeVersion")
			}).Should(BeTrue())
		})
	})
})
