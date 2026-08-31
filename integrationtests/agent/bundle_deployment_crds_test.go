package agent_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Helm chart defining CRDs and using lookup", Ordered, func() {
	var (
		env  *specEnv
		name string
	)

	createBundleDeployment := func(name, id, chart string) {
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
						Chart: chart,
						Values: &v1alpha1.GenericMap{
							Data: map[string]any{"value": "example-value"},
						},
					},
				},
			},
		}

		Expect(k8sClient.Create(ctx, &bundled)).ToNot(HaveOccurred())

		DeferCleanup(func() {
			Expect(k8sClient.Delete(context.TODO(), &v1alpha1.BundleDeployment{
				ObjectMeta: metav1.ObjectMeta{Namespace: clusterNS, Name: name},
			})).ToNot(HaveOccurred())
		})
	}

	BeforeAll(func() {
		env = &specEnv{namespace: createNamespace()}
	})

	When("the chart provides the CRDs for its custom resources", func() {
		BeforeAll(func() {
			name = "crds-and-lookup"
			createBundleDeployment(name, "crdsAndLookup", "crd-chart")
		})

		It("deploys the chart, although its CRDs cannot be installed during the dry run", func() {
			By("Making the BundleDeployment ready")
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())

			By("Deploying the custom resource defined by the chart's CRD")
			appConfig := &unstructured.Unstructured{}
			appConfig.SetGroupVersionKind(schema.GroupVersionKind{
				Group:   "stable.example.com",
				Version: "v1",
				Kind:    "AppConfig",
			})
			err := k8sClient.Get(
				ctx,
				types.NamespacedName{Namespace: env.namespace, Name: "frontend-config"},
				appConfig,
			)
			Expect(err).ToNot(HaveOccurred())

			By("Resolving the lookup function against the cluster")
			cm, err := env.getConfigMap("lookup-chart-config")
			Expect(err).ToNot(HaveOccurred())
			Expect(cm.Data["namespace"]).To(Equal(env.namespace))
		})
	})

	When("the chart does not provide the CRDs for its custom resources", func() {
		BeforeAll(func() {
			name = "missing-crd-and-lookup"
			createBundleDeployment(name, "missingCRDAndLookup", "missing-crd-chart")
		})

		It("reports the missing CRD in the BundleDeployment status", func() {
			Eventually(func(g Gomega) {
				bd := &v1alpha1.BundleDeployment{}
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
				g.Expect(err).ToNot(HaveOccurred())

				checkCondition(g, bd.Status.Conditions, "Deployed", "False", `no matches for kind "UnknownConfig"`)
			}).Should(Succeed())

			Expect(env.isBundleDeploymentReadyAndNotModified(name)).To(BeFalse())
		})
	})
})
