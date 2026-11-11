package agent_test

import (
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("Upgrade conflicts", func() {
	var (
		namespace string
		env       adoptEnv
	)

	BeforeEach(func() {
		namespace = createNamespace()
		env = adoptEnv{namespace: namespace, env: &specEnv{namespace: namespace}}
	})

	When("another field manager owns a field rendered by the bundle deployment", func() {
		It("takes ownership of the field on upgrade instead of failing with a conflict", func() {
			env.createBundleDeployment("force-conflicts", false)

			By("checking that the release was installed with server-side apply")
			var fleetManager string
			env.assertConfigMap(func(g Gomega, cm corev1.ConfigMap) {
				owners := dataKeyOwners(g, cm, "key")
				g.Expect(owners).To(HaveLen(1))
				g.Expect(owners[0].Operation).To(Equal(metav1.ManagedFieldsOperationApply))
				fleetManager = owners[0].Manager
			})

			By("applying a different value for a rendered field with another field manager")
			Expect(k8sClient.Apply(
				ctx,
				corev1ac.ConfigMap("cm1", namespace).WithData(map[string]string{"key": "other"}),
				client.FieldOwner("other-manager"),
				client.ForceOwnership,
			)).To(Succeed())

			By("upgrading the release")
			Eventually(func(g Gomega) {
				bd := &v1alpha1.BundleDeployment{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: clusterNS, Name: "force-conflicts"}, bd)).To(Succeed())
				bd.Spec.DeploymentID = "BundleDeploymentConfigMapV2"
				g.Expect(k8sClient.Update(ctx, bd)).To(Succeed())
			}).Should(Succeed())

			env.assertBundleDeployment("force-conflicts", func(bd *v1alpha1.BundleDeployment) error {
				if bd.Status.AppliedDeploymentID != "BundleDeploymentConfigMapV2" {
					return fmt.Errorf("upgrade not applied, status: %+v", bd.Status)
				}
				return nil
			})
			env.assertConfigMap(func(g Gomega, cm corev1.ConfigMap) {
				g.Expect(cm.Data).To(Equal(map[string]string{"key": "value", "key2": "value2"}))
				owners := dataKeyOwners(g, cm, "key")
				g.Expect(owners).To(HaveLen(1))
				g.Expect(owners[0].Manager).To(Equal(fleetManager))
				g.Expect(owners[0].Operation).To(Equal(metav1.ManagedFieldsOperationApply))
			})
		})
	})
})

// dataKeyOwners returns the managed fields entries which own the given data
// key of the ConfigMap.
func dataKeyOwners(g Gomega, cm corev1.ConfigMap, key string) []metav1.ManagedFieldsEntry {
	var owners []metav1.ManagedFieldsEntry
	for _, mf := range cm.ManagedFields {
		if mf.FieldsV1 == nil {
			continue
		}
		fields := map[string]map[string]any{}
		g.Expect(json.Unmarshal(mf.FieldsV1.GetRawBytes(), &fields)).To(Succeed())
		if _, ok := fields["f:data"]["f:"+key]; ok {
			owners = append(owners, mf)
		}
	}
	return owners
}
