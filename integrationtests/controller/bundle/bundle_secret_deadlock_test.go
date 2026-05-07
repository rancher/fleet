package bundle

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/helmvalues"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("BD secret / ValuesHash inconsistency deadlock", func() {
	var (
		bundleNS  string
		clusterNS string
		cluster   *fleet.Cluster
		bundle    *fleet.Bundle
		testID    string
	)

	BeforeEach(func() {
		var err error
		testID, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		if len(testID) > 8 {
			testID = testID[:8]
		}

		bundleNS = "bdl-bns-" + testID
		clusterNS = "bdl-cns-" + testID

		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: bundleNS},
		})).ToNot(HaveOccurred())
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: clusterNS},
		})).ToNot(HaveOccurred())

		cluster, err = utils.CreateCluster(ctx, k8sClient, "test-cluster-"+testID, bundleNS,
			map[string]string{"bdl-env": testID}, clusterNS)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		if bundle != nil {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, bundle))).To(Succeed())
			// Wait for BDs to be cleaned up before deleting namespaces.
			Eventually(func(g Gomega) {
				bdList := &fleet.BundleDeploymentList{}
				g.Expect(k8sClient.List(ctx, bdList, client.InNamespace(clusterNS))).To(Succeed())
				g.Expect(bdList.Items).To(BeEmpty())
			}, 30*time.Second, time.Second).Should(Succeed())
		}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, cluster))).To(Succeed())
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: bundleNS}}))).To(Succeed())
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: clusterNS}}))).To(Succeed())
	})

	// createBundleWithValues builds a bundle that targets the test cluster with the given Helm values.
	createBundleWithValues := func(name string, values map[string]interface{}) *fleet.Bundle {
		b := &fleet.Bundle{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: bundleNS},
			Spec: fleet.BundleSpec{
				Targets: []fleet.BundleTarget{
					{
						ClusterSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"bdl-env": testID},
						},
						BundleDeploymentOptions: fleet.BundleDeploymentOptions{
							Helm: &fleet.HelmOptions{
								Values: &fleet.GenericMap{Data: values},
							},
						},
					},
				},
				Resources: []fleet.BundleResource{{
					Name:    "test.yaml",
					Content: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\ndata:\n  key: value",
				}},
			},
		}
		Expect(k8sClient.Create(ctx, b)).ToNot(HaveOccurred())
		return b
	}

	// waitForBD waits until the BundleDeployment for bundleName has a non-empty ValuesHash,
	// meaning the controller completed at least one successful reconcile and wrote the secret.
	waitForBD := func(bundleName string) *fleet.BundleDeployment {
		var bd *fleet.BundleDeployment
		Eventually(func(g Gomega) {
			bdList := &fleet.BundleDeploymentList{}
			g.Expect(k8sClient.List(ctx, bdList, client.InNamespace(clusterNS))).To(Succeed())
			for i := range bdList.Items {
				item := &bdList.Items[i]
				if item.Labels[fleet.BundleLabel] == bundleName {
					g.Expect(item.Spec.ValuesHash).ToNot(BeEmpty(),
						"BD should have a ValuesHash set by manageOptionsSecret")
					bd = item
					return
				}
			}
			g.Expect(false).To(BeTrue(), "BundleDeployment not found for bundle "+bundleName)
		}).Should(Succeed())
		return bd
	}

	// corruptBDSecret overwrites the BD secret with data that hashes differently from the BD's
	// current ValuesHash, simulating the state left when manageOptionsSecret succeeded but
	// createBundleDeployment failed in a previous reconcile cycle.
	corruptBDSecret := func(bd *fleet.BundleDeployment) {
		staleValues := []byte(`replicas: 999`)
		Eventually(func(g Gomega) {
			secret := &corev1.Secret{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: bd.Name, Namespace: bd.Namespace,
			}, secret)).To(Succeed())

			// Sanity check: hashes must be consistent before we corrupt.
			h := helmvalues.HashOptions(secret.Data[helmvalues.ValuesKey], secret.Data[helmvalues.StagedValuesKey])
			g.Expect(h).To(Equal(bd.Spec.ValuesHash), "pre-condition: secret and BD must be consistent")

			secret.Data[helmvalues.ValuesKey] = staleValues
			g.Expect(k8sClient.Update(ctx, secret)).To(Succeed())
		}).Should(Succeed())
	}

	// getBundleReadyMessage returns the message from the bundle's Ready condition, or "" if absent.
	getBundleReadyMessage := func(b *fleet.Bundle) string {
		latest := &fleet.Bundle{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(b), latest); err != nil {
			return ""
		}
		for _, c := range latest.Status.Conditions {
			if c.Type == "Ready" {
				return c.Message
			}
		}
		return ""
	}

	// When the BD secret and BD.Spec.ValuesHash are inconsistent, the bundle
	// controller should self-heal: it must re-synchronise the secret and clear
	// the targeting error without requiring a manual patch.
	It("self-heals after the BD secret content diverges from ValuesHash", func() {
		By("creating a bundle with Helm values so manageOptionsSecret writes a secret")
		bundleName := "bdl-selfheal-" + testID
		bundle = createBundleWithValues(bundleName, map[string]interface{}{"data": 8})

		By("waiting for the BD and its options secret to be created with a consistent hash")
		bd := waitForBD(bundleName)

		By("corrupting the BD secret (simulates: manageOptionsSecret ran but createBundleDeployment failed)")
		corruptBDSecret(bd)

		// Verify the mismatch is real before letting the controller see it.
		secret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: bd.Name, Namespace: bd.Namespace}, secret)).To(Succeed())
		actualHash := helmvalues.HashOptions(secret.Data[helmvalues.ValuesKey], secret.Data[helmvalues.StagedValuesKey])
		Expect(actualHash).ToNot(Equal(bd.Spec.ValuesHash), "pre-condition: hashes must differ after corruption")

		By("triggering a bundle reconcile so the controller detects the mismatch")
		Eventually(func(g Gomega) {
			latest := &fleet.Bundle{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bundle), latest)).To(Succeed())
			if latest.Annotations == nil {
				latest.Annotations = map[string]string{}
			}
			latest.Annotations["test/trigger"] = "1"
			g.Expect(k8sClient.Update(ctx, latest)).To(Succeed())
		}).Should(Succeed())

		By("confirming the hash-mismatch targeting error appears on the bundle")
		Eventually(func(g Gomega) {
			msg := getBundleReadyMessage(bundle)
			g.Expect(msg).To(ContainSubstring("hash mismatch between secret and bundledeployment"),
				"the hash-mismatch error should appear after the controller reconciles with the corrupted secret")
		}).Should(Succeed())

		// The error should eventually clear without any further
		// external intervention.
		By("expecting the bundle to eventually clear the targeting error (self-heal)")
		Eventually(func(g Gomega) {
			msg := getBundleReadyMessage(bundle)
			g.Expect(msg).ToNot(ContainSubstring("hash mismatch between secret and bundledeployment"),
				"the hash-mismatch targeting error should clear once the controller self-heals")
		}).Should(Succeed())
	})

	// After a secret/ValuesHash inconsistency, a subsequent bundle update (e.g. a
	// new GitRepo commit with new values) should be applied to the BD.
	It("applies new bundle values after recovering from a secret/ValuesHash inconsistency", func() {
		By("creating a bundle with initial Helm values")
		bundleName := "bdl-newvals-" + testID
		bundle = createBundleWithValues(bundleName, map[string]interface{}{"data": 8})

		By("waiting for the BD and its options secret to be created consistently")
		bd := waitForBD(bundleName)
		originalHash := bd.Spec.ValuesHash

		By("corrupting the BD secret to create the inconsistency")
		corruptBDSecret(bd)

		By("updating the bundle with new Helm values (simulates a new GitRepo commit)")
		Eventually(func(g Gomega) {
			latest := &fleet.Bundle{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bundle), latest)).To(Succeed())
			latest.Spec.Targets[0].BundleDeploymentOptions.Helm.Values = &fleet.GenericMap{
				Data: map[string]interface{}{"data": 25},
			}
			g.Expect(k8sClient.Update(ctx, latest)).To(Succeed())
		}).Should(Succeed())

		// After the controller reconciles the new values, the BD's
		// ValuesHash should change from the original value to a new one reflecting the
		// updated values.
		By("expecting the BD's ValuesHash to change to reflect the new values")
		Eventually(func(g Gomega) {
			latestBD := &fleet.BundleDeployment{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: bd.Name, Namespace: bd.Namespace,
			}, latestBD)).To(Succeed())
			g.Expect(latestBD.Spec.ValuesHash).ToNot(Equal(originalHash),
				"BD ValuesHash should be updated once the controller applies the new values")
		}).Should(Succeed())
	})
})
