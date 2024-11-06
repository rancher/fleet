package singlecluster_test

import (
	"strings"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = FDescribe("Helm deploy options", func() {
	var (
		asset string
		k     kubectl.Command
	)
	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
	})

	JustBeforeEach(func() {
		out, err := k.Apply("-f", testenv.AssetPath(asset))
		Expect(err).ToNot(HaveOccurred(), out)
	})

	AfterEach(func() {
		out, err := k.Delete("-f", testenv.AssetPath(asset))
		Expect(err).ToNot(HaveOccurred(), out)
	})

	Describe("namespaceLabels TargetCustomization", func() {
		// TODO add a test case without defaults
		BeforeEach(func() {
			asset = "single-cluster/ns-labels-target-customization.yaml"
		})
		When("namespaceLabels and namespaceAnnotations are set as targetCustomization ", func() {
			It("deploys the bundledeployment with the merged namespaceLabels and namespaceAnnotations", func() {
				By("setting the namespaces and annotations on the bundle deployment")
				out, err := k.Get("cluster", "local", "-o", "jsonpath={.status.namespace}")
				Expect(err).ToNot(HaveOccurred(), out)

				clusterNS := strings.TrimSpace(out)
				clusterK := k.Namespace(clusterNS)

				var bundleDeploymentName string

				Eventually(func(g Gomega) {
					bundleDeploymentNames, _ := clusterK.Get(
						"bundledeployments",
						"-o",
						"jsonpath={.items[*].metadata.name}",
					)

					for _, bdName := range strings.Split(bundleDeploymentNames, " ") {
						if strings.HasPrefix(bdName, "test-target-customization-namespace-labels") {
							bundleDeploymentName = bdName
							break
						}
					}

					g.Expect(bundleDeploymentName).ToNot(BeEmpty())
				}).Should(Succeed())

				Eventually(func(g Gomega) {
					labels, err := clusterK.Get(
						"bundledeployments",
						bundleDeploymentName,
						"-o",
						"jsonpath={.spec.options.namespaceLabels}",
					)

					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(labels).To(Equal(`{"foo":"bar","this.is/a":"test"}`))

					annot, err := clusterK.Get(
						"bundledeployments",
						bundleDeploymentName,
						"-o",
						"jsonpath={.spec.options.namespaceAnnotations}",
					)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(annot).To(Equal(`{"foo":"bar","this.is/another":"test"}`))
				}).Should(Succeed())

				By("setting the namespaces and annotations on the created namespace")
				Eventually(func(g Gomega) {
					labels, err := k.Get("ns", "ns-1", "-o", "jsonpath={.metadata.labels}")
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(labels).To(Equal(`{"foo":"bar","kubernetes.io/metadata.name":"ns-1","this.is/a":"test"}`))

					ann, err := k.Get("ns", "ns-1", "-o", "jsonpath={.metadata.annotations}")
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(ann).To(Equal(`{"foo":"bar","this.is/another":"test"}`))
				}).Should(Succeed())
			})
		})
	})

})
