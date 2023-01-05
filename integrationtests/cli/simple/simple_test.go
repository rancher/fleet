package simple

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/integrationtests/cli"
	"github.com/rancher/fleet/modules/cli/apply"
)

var _ = Describe("Fleet apply with yaml resources", Ordered, func() {
	When("apply a folder with yaml resources", func() {
		It("fleet apply is called", func() {
			err := fleetApply("simple", []string{cli.AssetsPath + "simple"}, &apply.Options{})
			Expect(err).NotTo(HaveOccurred())
		})

		It("then Bundle is created with all the resources", func() {
			Eventually(isBundlePresentAndHasResources).Should(BeTrue())
		})
	})
})

func isBundlePresentAndHasResources() bool {
	bundle, err := cli.GetBundleFromOutput(buf)
	Expect(err).NotTo(HaveOccurred())
	isSvcPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"simple/svc.yaml", bundle.Spec.Resources)
	Expect(err).NotTo(HaveOccurred())
	isDeploymentPresent, err := cli.IsResourcePresentInBundle(cli.AssetsPath+"simple/deployment.yaml", bundle.Spec.Resources)
	Expect(err).NotTo(HaveOccurred())

	return isSvcPresent && isDeploymentPresent
}
