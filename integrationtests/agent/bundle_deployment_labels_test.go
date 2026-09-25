package agent_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Validate labels on deployed resources", Ordered, func() {
	var (
		env  *specEnv
		name string
	)

	// createBundleDeployment deploys a single ConfigMap, with bdLabels standing
	// in for the source labels the upstream controller stamps onto a
	// BundleDeployment.
	createBundleDeployment := func(name string, bdLabels map[string]string) {
		bundled := v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: clusterNS,
				Labels:    bdLabels,
			},
			Spec: v1alpha1.BundleDeploymentSpec{
				DeploymentID: "BundleDeploymentConfigMap",
				Options: v1alpha1.BundleDeploymentOptions{
					DefaultNamespace: env.namespace,
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

	When("the bundle deployment comes from a HelmOp", func() {
		BeforeAll(func() {
			env = &specEnv{namespace: createNamespace()}
			name = "by-helmop"
			createBundleDeployment(name, map[string]string{
				v1alpha1.HelmOpLabel:          "kafka",
				v1alpha1.BundleNamespaceLabel: "fleet-default",
			})
		})

		It("labels the deployed resources as managed by that HelmOp", func() {
			Eventually(func(g Gomega) {
				cm, err := env.getConfigMap("cm1")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cm.Labels).To(HaveKeyWithValue(v1alpha1.ManagedByKindLabel, v1alpha1.ManagedByKindHelmOp))
				g.Expect(cm.Labels).To(HaveKeyWithValue(v1alpha1.ManagedByNamespaceLabel, "fleet-default"))
				g.Expect(cm.Labels).To(HaveKeyWithValue(v1alpha1.ManagedByNameLabel, "kafka"))
			}).Should(Succeed())
		})
	})

	When("the bundle deployment comes from a GitRepo", func() {
		BeforeAll(func() {
			env = &specEnv{namespace: createNamespace()}
			name = "by-gitrepo"
			createBundleDeployment(name, map[string]string{
				v1alpha1.RepoLabel:            "label-trace",
				v1alpha1.BundleNamespaceLabel: "fleet-local",
			})
		})

		It("labels the deployed resources as managed by that GitRepo", func() {
			Eventually(func(g Gomega) {
				cm, err := env.getConfigMap("cm1")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cm.Labels).To(HaveKeyWithValue(v1alpha1.ManagedByKindLabel, v1alpha1.ManagedByKindGitRepo))
				g.Expect(cm.Labels).To(HaveKeyWithValue(v1alpha1.ManagedByNamespaceLabel, "fleet-local"))
				g.Expect(cm.Labels).To(HaveKeyWithValue(v1alpha1.ManagedByNameLabel, "label-trace"))
			}).Should(Succeed())
		})
	})

	When("the bundle deployment carries no Fleet source labels", func() {
		BeforeAll(func() {
			env = &specEnv{namespace: createNamespace()}
			name = "by-none"
			createBundleDeployment(name, map[string]string{
				"objectset.rio.cattle.io/hash": "abc123",
			})
		})

		It("deploys the resources without labels", func() {
			By("Making the BundleDeployment ready")
			Eventually(env.isBundleDeploymentReadyAndNotModified).WithArguments(name).Should(BeTrue())

			cm, err := env.getConfigMap("cm1")
			Expect(err).ToNot(HaveOccurred())
			Expect(cm.Labels).ToNot(HaveKey(v1alpha1.ManagedByKindLabel))
			Expect(cm.Labels).ToNot(HaveKey(v1alpha1.ManagedByNamespaceLabel))
			Expect(cm.Labels).ToNot(HaveKey(v1alpha1.ManagedByNameLabel))
		})
	})
})
