package agentmanagement_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/resources"
	"github.com/rancher/fleet/internal/config"
)

var _ = Describe("AgentManagement harness smoke test", func() {
	It("starts the controllers without error", func() {
		// Reaching here means BeforeSuite completed — envtest started,
		// Wrangler controllers registered and started.
		Expect(true).To(BeTrue())
	})

	It("sets global config after controller startup", func() {
		// config.Register (called inside controllers.Register) does an
		// initial config.Lookup and calls config.SetAndTrigger so
		// config.Get() must return a non-nil value here.
		Expect(config.Get()).NotTo(BeNil())
	})

	It("creates the system namespace via ApplyBootstrapResources", func() {
		// resources.ApplyBootstrapResources runs synchronously in Register.
		namespaceExists(systemNamespace).Should(Succeed())
	})

	It("creates the system registration namespace via ApplyBootstrapResources", func() {
		// The registration namespace is derived from the system namespace:
		// "cattle-fleet-system" → "cattle-fleet-clusters-system".
		regNS := "cattle-fleet-clusters-system"
		namespaceExists(regNS).Should(Succeed())
	})

	It("creates the fleet-bundle-deployment ClusterRole", func() {
		cr := clusterRole(resources.BundleDeploymentClusterRole)
		objectExists(cr).Should(Succeed())
	})

	It("creates the fleet-content ClusterRole", func() {
		cr := clusterRole(resources.ContentClusterRole)
		objectExists(cr).Should(Succeed())
	})
})
