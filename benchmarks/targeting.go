package benchmarks

import (
	"github.com/rancher/fleet/benchmarks/record"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gm "github.com/onsi/gomega/gmeasure"
)

// create-50-bundle
// create-150-bundle
var _ = Context("Benchmarks Targeting", func() {
	var (
		n        int
		manifest string
	)

	BeforeEach(func() {
		clusters := &v1alpha1.ClusterList{}
		Expect(k8sClient.List(ctx, clusters, client.InNamespace(workspace), client.MatchingLabels{
			"fleet.cattle.io/benchmark": "true",
		})).To(Succeed())
		n = len(clusters.Items)
	})

	Describe("Adding 50 Bundles", Label("create-50-bundle"), func() {
		BeforeEach(func() {
			name = "create-50-bundle"
			info = "creating 50 bundles targeting each cluster"
			manifest = assetPath("create-bundle/bundles50.yaml")
			err := generateAsset(
				manifest,
				assetPath("create-bundle/bundles.tmpl.yaml"),
				struct{ Max int }{50})
			Expect(err).ToNot(HaveOccurred())
		})

		It("creates 50 bundledeployments", func() {
			DeferCleanup(func() {
				_, _ = k.Delete("-f", manifest)
			})

			experiment.MeasureDuration("TotalDuration", func() {
				record.MemoryUsage(experiment, "MemDuring")

				_, _ = k.Apply("-f", manifest)
				Eventually(func(g Gomega) {
					list := &v1alpha1.BundleDeploymentList{}
					err := k8sClient.List(ctx, list, client.MatchingLabels{
						GroupLabel: name,
					})
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(list.Items).To(HaveLen(n * 50))
				}).Should(Succeed())
			}, gm.Style("{{bold}}"))
		})
	})

	Describe("Adding 150 Bundles", Label("create-150-bundle"), func() {
		BeforeEach(func() {
			name = "create-150-bundle"
			info = "creating 150 bundles targeting each cluster"
			manifest = assetPath("create-bundle/bundles150.yaml")
			err := generateAsset(
				manifest,
				assetPath("create-bundle/bundles.tmpl.yaml"),
				struct{ Max int }{150})
			Expect(err).ToNot(HaveOccurred())
		})

		It("creates 150 bundledeployments", func() {
			DeferCleanup(func() {
				_, _ = k.Delete("-f", manifest)
			})

			experiment.MeasureDuration("TotalDuration", func() {
				record.MemoryUsage(experiment, "MemDuring")

				_, _ = k.Apply("-f", manifest)
				Eventually(func(g Gomega) {
					list := &v1alpha1.BundleDeploymentList{}
					err := k8sClient.List(ctx, list, client.MatchingLabels{
						GroupLabel: name,
					})
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(list.Items).To(HaveLen(n * 150))
				}).Should(Succeed())
			}, gm.Style("{{bold}}"))
		})
	})
})
