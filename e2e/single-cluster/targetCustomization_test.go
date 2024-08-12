package singlecluster_test

import (
	"strings"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Helm deploy options", func() {
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
		BeforeEach(func() {
			asset = "single-cluster/namespaceLabels_targetCustomization.yaml"
		})
		When("namespaceLabels set as targetCustomization ", func() {
			It("deploy the bundledeployment with the merged namespaceLabels", func() {
				Eventually(func() string {
					bundleDeploymentNames, _ := k.Namespace("cluster-fleet-local-local-1a3d67d0a899").Get("bundledeployments", "-o", "jsonpath={.items[*].metadata.name}")

					var bundleDeploymentName string
					for _, podName := range strings.Split(bundleDeploymentNames, " ") {
						if strings.HasPrefix(podName, "test-namespacelabels-targetcustomization") {
							bundleDeploymentName = podName
							break
						}
					}
					if bundleDeploymentName == "" {
						return "nil"
					}

					bundleDeploymentNamespacesLabels, _ := k.Namespace("cluster-fleet-local-local-1a3d67d0a899").Get("bundledeployments", bundleDeploymentName, "-o", "jsonpath={.spec.options.namespaceLabels}")
					return bundleDeploymentNamespacesLabels
				}).Should(Equal(`{"foo":"bar","this.is/a":"test"}`))

				Eventually(func() string {
					bundleDeploymentNames, _ := k.Namespace("cluster-fleet-local-local-1a3d67d0a899").Get("bundledeployments", "-o", "jsonpath={.items[*].metadata.name}")

					var bundleDeploymentName string
					for _, podName := range strings.Split(bundleDeploymentNames, " ") {
						if strings.HasPrefix(podName, "test-namespacelabels-targetcustomization") {
							bundleDeploymentName = podName
							break
						}
					}
					if bundleDeploymentName == "" {
						return "nil"
					}

					bundleDeploymentNamespacesLabels, _ := k.Namespace("cluster-fleet-local-local-1a3d67d0a899").Get("bundledeployments", bundleDeploymentName, "-o", "jsonpath={.spec.options.namespaceAnnotations}")
					return bundleDeploymentNamespacesLabels
				}).Should(Equal(`{"foo":"bar","this.is/a":"test"}`))

			})
		})
	})

})
