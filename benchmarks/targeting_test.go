package benchmarks_test

import (
	"github.com/rancher/fleet/benchmarks/record"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// create-1-bundle
// create-50-bundle
var _ = Context("Benchmarks Targeting", func() {
	var n int

	BeforeEach(func() {
		clusters := &v1alpha1.ClusterList{}
		Expect(k8sClient.List(ctx, clusters, client.InNamespace(workspace), client.MatchingLabels{
			"fleet.cattle.io/benchmark": "true",
		})).To(Succeed())
		n = len(clusters.Items)
	})

	Describe("Adding 1 Bundle results in BundleDeployments", Label("create-1-bundle"), func() {
		BeforeEach(func() {
			name = "create-1-bundle"
			info = "creating one bundle targeting each cluster"
		})

		It("creates one bundledeployment", func() {
			DeferCleanup(func() {
				_, _ = k.Delete("-f", AssetPath(name, "bundle.yaml"))
			})

			experiment.MeasureDuration("TotalDuration", func() {
				record.MemoryUsage(experiment, "MemDuring")

				_, _ = k.Apply("-f", AssetPath(name, "bundle.yaml"))
				Eventually(func(g Gomega) {
					list := &v1alpha1.BundleDeploymentList{}
					err := k8sClient.List(ctx, list, client.MatchingLabels{
						GroupLabel: name,
					})
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(len(list.Items)).To(Equal(n))
				}).Should(Succeed())
			})
		})
	})

	Describe("Adding 50 Bundles", Label("create-50-bundle"), func() {
		BeforeEach(func() {
			name = "create-50-bundle"
			info = "creating 50 bundles targeting each cluster"
		})

		It("creates 50 bundledeployments", func() {
			DeferCleanup(func() {
				_, _ = k.Delete("-f", AssetPath(name, "bundles.yaml"))
			})

			experiment.MeasureDuration("TotalDuration", func() {
				record.MemoryUsage(experiment, "MemDuring")

				_, _ = k.Apply("-f", AssetPath(name, "bundles.yaml"))
				Eventually(func(g Gomega) {
					list := &v1alpha1.BundleDeploymentList{}
					err := k8sClient.List(ctx, list, client.MatchingLabels{
						GroupLabel: name,
					})
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(len(list.Items)).To(Equal(n * 50))
				}).Should(Succeed())
			})
		})
	})
})
