package bundle

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Bundle with helm.preserveOnReplace", Ordered, func() {
	const bundleName = "preserve-on-replace"

	var (
		pvcRule = v1alpha1.PreserveOnReplaceRule{
			APIVersion: "v1",
			Kind:       "PersistentVolumeClaim",
			Paths:      []v1alpha1.PreservedPath{{Path: "/spec/volumeName"}},
		}
		jobRule = v1alpha1.PreserveOnReplaceRule{
			APIVersion: "batch/v1",
			Kind:       "Job",
			Paths:      []v1alpha1.PreservedPath{{Path: "/spec/selector"}},
		}
	)

	BeforeAll(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).ToNot(HaveOccurred())

		createClustersAndClusterGroups()

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, ns)).ToNot(HaveOccurred())
		})
	})

	// bundleDeployments returns the bundle's deployments by cluster name.
	bundleDeployments := func(g Gomega) map[string]v1alpha1.BundleDeployment {
		bdList := &v1alpha1.BundleDeploymentList{}
		g.Expect(k8sClient.List(ctx, bdList, client.MatchingLabelsSelector{Selector: labels.SelectorFromSet(map[string]string{
			"fleet.cattle.io/bundle-name":      bundleName,
			"fleet.cattle.io/bundle-namespace": namespace,
		})})).To(Succeed())
		bds := map[string]v1alpha1.BundleDeployment{}
		for _, bd := range bdList.Items {
			bds[bd.Labels["fleet.cattle.io/cluster"]] = bd
		}
		g.Expect(bds).To(HaveLen(3))
		return bds
	}

	rules := func(opts v1alpha1.BundleDeploymentOptions) []v1alpha1.PreserveOnReplaceRule {
		if opts.Helm == nil {
			return nil
		}
		return opts.Helm.PreserveOnReplace
	}

	It("merges target customizations and creates a new deployment ID when the rules change", func() {
		bundle := &v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{Name: bundleName, Namespace: namespace},
			Spec: v1alpha1.BundleSpec{
				BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
					Helm: &v1alpha1.HelmOptions{Force: true},
				},
				Targets: []v1alpha1.BundleTarget{
					{
						Name:        "one",
						ClusterName: "one",
						BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
							Helm: &v1alpha1.HelmOptions{PreserveOnReplace: []v1alpha1.PreserveOnReplaceRule{pvcRule}},
						},
					},
					{Name: "all", ClusterGroup: "all"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, bundle)).To(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, bundle))).To(Succeed())
			cleanupBundleDeployments(bundleName, namespace)
		})

		By("applying the rule of a target customization to the matching target only")
		deploymentIDs := map[string]string{}
		Eventually(func(g Gomega) {
			for cluster, bd := range bundleDeployments(g) {
				var want []v1alpha1.PreserveOnReplaceRule
				if cluster == "one" {
					want = []v1alpha1.PreserveOnReplaceRule{pvcRule}
				}
				g.Expect(rules(bd.Spec.Options)).To(Equal(want), cluster)
				g.Expect(rules(bd.Spec.StagedOptions)).To(Equal(want), cluster)
				g.Expect(bd.Spec.Options.Helm.Force).To(BeTrue(), cluster)
				g.Expect(bd.Spec.DeploymentID).ToNot(BeEmpty())
				deploymentIDs[cluster] = bd.Spec.DeploymentID
			}
		}).Should(Succeed())

		By("changing only helm.preserveOnReplace, adding a bundle rule for the same objects as the customization's")
		changedPVCRule := pvcRule
		changedPVCRule.Paths = []v1alpha1.PreservedPath{{Path: "/spec/volumeName"}, {Path: "/spec/storageClassName"}}
		basePVCRule := pvcRule
		basePVCRule.Paths = []v1alpha1.PreservedPath{{Path: "/spec/volumeMode"}}
		Eventually(func() error {
			b := &v1alpha1.Bundle{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: bundleName}, b); err != nil {
				return err
			}
			b.Spec.Helm.PreserveOnReplace = []v1alpha1.PreserveOnReplaceRule{jobRule, basePVCRule}
			b.Spec.Targets[0].Helm.PreserveOnReplace = []v1alpha1.PreserveOnReplaceRule{changedPVCRule}
			return k8sClient.Update(ctx, b)
		}).Should(Succeed())

		Eventually(func(g Gomega) {
			for cluster, bd := range bundleDeployments(g) {
				want := []v1alpha1.PreserveOnReplaceRule{jobRule, basePVCRule}
				if cluster == "one" {
					// Added to the bundle's rules, not replacing basePVCRule.
					want = append(want, changedPVCRule)
				}
				g.Expect(rules(bd.Spec.Options)).To(Equal(want), cluster)
				g.Expect(rules(bd.Spec.StagedOptions)).To(Equal(want), cluster)
				g.Expect(bd.Spec.DeploymentID).ToNot(Equal(deploymentIDs[cluster]), cluster)
				g.Expect(bd.Spec.StagedDeploymentID).To(Equal(bd.Spec.DeploymentID), cluster)
			}
		}).Should(Succeed())
	})
})
