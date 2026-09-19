package multicluster_test

import (
	"fmt"
	"time"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// helmOpParams represents the fields which will vary over test cases implemented in this driver.
type helmOpParams struct {
	name            string
	deleteNamespace bool
	keepResources   bool
	targetNamespace string
}

var _ = Describe("Target namespace deletion through deleteNamespace", func() {
	var (
		k  kubectl.Command
		kd kubectl.Command

		asset           string
		name            string
		deleteNamespace bool
		targetNamespace string
		cmName          = "test-simple-chart-config"
	)

	BeforeEach(func() {
		k = env.Kubectl.Context(env.Upstream)
		kd = env.Kubectl.Context(env.Downstream).Namespace("fleet-default")
	})

	JustBeforeEach(func() {
		setupHelmOp(k, asset, helmOpParams{
			name:            name,
			deleteNamespace: deleteNamespace,
			targetNamespace: targetNamespace,
		})
	})

	When("deleteNamespace is set to true", func() {
		BeforeEach(func() {
			asset = "multi-cluster/helmop_delete_namespace.yaml"
			name = "helmop-delete-namespace"
			deleteNamespace = true
			targetNamespace = "test-delns"
		})

		It("deletes the target namespace on the downstream cluster when deleting the GitRepo", func() {
			By("checking that the workload has been deployed")
			Eventually(func(g Gomega) {
				cms, err := kd.Namespace(targetNamespace).Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring(cmName))
			}).Should(Succeed())

			By("deleting the workload")
			_, err := k.Namespace(env.ClusterRegistrationNamespace).Delete("helmop", name)
			Expect(err).ToNot(HaveOccurred())

			By("checking that the target namespace no longer exists")
			Eventually(func(g Gomega) {
				nss, err := kd.Get("namespaces")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(nss).ToNot(ContainSubstring(targetNamespace))
			}).Should(Succeed())
		})
	})

	When("deleteNamespace is set to false", func() {
		BeforeEach(func() {
			asset = "multi-cluster/helmop_delete_namespace.yaml"
			name = "helmop-no-delete-namespace"
			deleteNamespace = false
			targetNamespace = "test-no-delns"
		})

		It("keeps the target namespace on the downstream cluster when deleting the GitRepo", func() {
			By("checking that the workload has been deployed")
			Eventually(func(g Gomega) {
				cms, err := kd.Namespace(targetNamespace).Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring(cmName))
			}).Should(Succeed())

			By("deleting the workload")
			_, err := k.Namespace(env.ClusterRegistrationNamespace).Delete("helmop", name)
			Expect(err).ToNot(HaveOccurred())

			By("checking that the target namespace still exists")
			Consistently(func(g Gomega) {
				nss, err := kd.Get("namespaces")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(nss).To(ContainSubstring(targetNamespace))
			}).WithTimeout(10 * time.Second).WithPolling(2 * time.Second).Should(Succeed())
		})
	})
})

var _ = Describe("Target namespace deletion edge cases", func() {
	var (
		k  kubectl.Command
		kd kubectl.Command

		asset  string
		cmName = "test-simple-chart-config"
	)

	BeforeEach(func() {
		k = env.Kubectl.Context(env.Upstream)
		kd = env.Kubectl.Context(env.Downstream).Namespace("fleet-default")
	})

	DescribeTable("deleteNamespace is set to true, but the target namespace should not be deleted", func(hop helmOpParams) {
		asset = "multi-cluster/helmop_delete_namespace.yaml"

		By("setting up a workload")
		setupHelmOp(k, asset, hop)

		By("checking that the workload has been deployed")
		Eventually(func(g Gomega) {
			cms, err := kd.Namespace(hop.targetNamespace).Get("configmaps")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(cms).To(ContainSubstring(cmName))
		}).Should(Succeed())

		By("deleting the workload")
		_, err := k.Namespace(env.ClusterRegistrationNamespace).Delete("helmop", hop.name)
		Expect(err).ToNot(HaveOccurred())

		By("checking that the target namespace still exists")
		Consistently(func(g Gomega) {
			nss, err := kd.Get("namespaces")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(nss).To(ContainSubstring(hop.targetNamespace))
		}).WithTimeout(10 * time.Second).WithPolling(2 * time.Second).Should(Succeed())
	},
		Entry(
			"target is kube-system",
			helmOpParams{
				name:            "target-kube-system",
				deleteNamespace: true,
				targetNamespace: "kube-system",
			},
		),
		Entry(
			"target is default",
			helmOpParams{
				name:            "target-default",
				deleteNamespace: true,
				targetNamespace: "default",
			},
		),
		Entry(
			"keepResources is true",
			helmOpParams{
				name:            "helmop-keepresources",
				deleteNamespace: true,
				targetNamespace: "test-target-ns",
				keepResources:   true,
			},
		),
	)
})

func setupHelmOp(k kubectl.Command, asset string, hop helmOpParams) {
	err := testenv.ApplyTemplate(k.Namespace(env.ClusterRegistrationNamespace), testenv.AssetPath(asset), struct {
		Name            string
		Namespace       string
		Repo            string
		Chart           string
		Version         string
		DeleteNamespace bool
		KeepResources   bool
		TargetNamespace string
	}{
		hop.name,
		env.ClusterRegistrationNamespace,
		"",
		"https://github.com/rancher/fleet/raw/refs/heads/main/integrationtests/cli/assets/helmrepository/config-chart-0.1.0.tgz",
		"",
		hop.deleteNamespace,
		hop.keepResources,
		hop.targetNamespace,
	})
	Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("failed to apply HelmOp: %v", err))
}
