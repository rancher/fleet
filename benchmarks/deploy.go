package benchmarks

import (
	"github.com/rancher/fleet/benchmarks/record"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gm "github.com/onsi/gomega/gmeasure"
)

// These experiments measure the time it takes to deploy a bundledeployment.
// However, bundledeployments cannot exist without bundles, so we create the
// bundles first, wait for targeting to be done and then unpause the bundles.
//
// create-1-bundledeployment-10-resources
// create-50-bundledeployment-500-resources
var _ = Context("Benchmarks Deploy", func() {
	var (
		clusters *v1alpha1.ClusterList
		n        int
		manifest string
	)

	BeforeEach(func() {
		clusters = &v1alpha1.ClusterList{}
		Expect(k8sClient.List(ctx, clusters, client.InNamespace(workspace), client.MatchingLabels{
			"fleet.cattle.io/benchmark": "true",
		})).To(Succeed())
		n = len(clusters.Items)
		Expect(n).ToNot(BeZero(), "you need at least one cluster labeled with fleet.cattle.io/benchmark=true")
	})

	Describe("Unpausing 1 BundleDeployments results in 10 Resources", Label("create-1-bundledeployment-10-resources"), func() {
		BeforeEach(func() {
			name = "create-1-bundledeployment-10-resources"
			info = "creating one bundledeployment, targeting each cluster"
		})

		It("creates one bundledeployment per cluster", func() {
			DeferCleanup(func() {
				_, _ = k.Delete("-f", assetPath(name, "bundle.yaml"))
			})

			By("preparing the paused bundles")
			_, err := k.Apply("-f", assetPath(name, "bundle.yaml"))
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				list := &v1alpha1.BundleDeploymentList{}
				err := k8sClient.List(ctx, list, client.MatchingLabels{
					GroupLabel: name,
				})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(list.Items).To(HaveLen(n))
			}).Should(Succeed())

			experiment.MeasureDuration("TotalDuration", func() {
				record.MemoryUsage(experiment, "MemDuring")

				// unpausing is part of the experiment, we don't want to miss reconciles
				bundle := &v1alpha1.Bundle{}
				err := k8sClient.Get(ctx, client.ObjectKey{Namespace: workspace, Name: name}, bundle)
				Expect(err).ToNot(HaveOccurred())

				orig := bundle.DeepCopy()
				bundle.Spec.Paused = false
				patch := client.MergeFrom(orig)
				err = k8sClient.Patch(ctx, bundle, patch)
				Expect(err).ToNot(HaveOccurred())

				Eventually(func(g Gomega) {
					bundle := &v1alpha1.Bundle{}
					err := k8sClient.Get(ctx, client.ObjectKey{Namespace: workspace, Name: name}, bundle)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(bundle.Status.Summary.DesiredReady).To(Equal(n))
					g.Expect(bundle.Status.Summary.Ready).To(Equal(n))
				}).Should(Succeed())
			}, gm.Style("{{bold}}"))
		})
	})

	Describe("Unpausing 50 BundleDeployments results in 500 Resources", Label("create-50-bundledeployment-500-resources"), func() {
		BeforeEach(func() {
			name = "create-50-bundledeployment-500-resources"
			info = "creating 50 bundledeployments, targeting each cluster"
			manifest = assetPath("create-bundledeployment-500-resources/bundles50.yaml")
			err := generateAsset(
				manifest,
				assetPath("create-bundledeployment-500-resources/bundles.tmpl.yaml"),
				struct{ Max int }{50})
			Expect(err).ToNot(HaveOccurred())
		})

		It("creates 50 bundledeployments", func() {
			DeferCleanup(func() {
				_, _ = k.Delete("-f", manifest)
			})

			By("preparing the paused bundles")
			_, _ = k.Apply("-f", manifest)
			Eventually(func(g Gomega) {
				list := &v1alpha1.BundleDeploymentList{}
				err := k8sClient.List(ctx, list, client.MatchingLabels{
					GroupLabel: name,
				})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(list.Items).To(HaveLen(n * 50))
			}).Should(Succeed())

			experiment.MeasureDuration("TotalDuration", func() {
				record.MemoryUsage(experiment, "MemDuring")

				list := &v1alpha1.BundleList{}
				err := k8sClient.List(ctx, list, client.MatchingLabels{
					GroupLabel: name,
				})
				Expect(err).ToNot(HaveOccurred())
				for _, bundle := range list.Items {
					orig := bundle.DeepCopy()
					bundle.Spec.Paused = false
					patch := client.MergeFrom(orig)
					err = k8sClient.Patch(ctx, &bundle, patch)
					Expect(err).ToNot(HaveOccurred())
				}

				Eventually(func(g Gomega) {
					for _, c := range clusters.Items {
						cluster := &v1alpha1.Cluster{}
						err := k8sClient.Get(ctx, client.ObjectKey{Namespace: workspace, Name: c.Name}, cluster)
						g.Expect(err).ToNot(HaveOccurred())
						// +1 because we expect the agent to be ready as well
						g.Expect(cluster.Status.Summary.DesiredReady).To(Equal(50 + 1))
						g.Expect(cluster.Status.Summary.Ready).To(Equal(50 + 1))
					}
				}).Should(Succeed())
			}, gm.Style("{{bold}}"))
		})
	})
})
