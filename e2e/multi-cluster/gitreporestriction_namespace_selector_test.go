package multicluster_test

import (
	"math/rand"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

// This test verifies that allowedTargetNamespaceSelector in a GitRepoRestriction
// controls whether a bundle is deployed to a downstream cluster's namespace.
// The selector is evaluated by the downstream agent: the target namespace must
// exist and carry all labels required by the selector.
var _ = Describe("GitRepoRestriction allowedTargetNamespaceSelector", func() {
	var (
		k           kubectl.Command
		kd          kubectl.Command
		namespace   string
		gitrepoName string
		r           = rand.New(rand.NewSource(GinkgoRandomSeed()))
	)

	type TemplateData struct {
		Name                         string
		TargetNamespace              string
		ClusterRegistrationNamespace string
	}

	BeforeEach(func() {
		k = env.Kubectl.Context(env.Upstream)
		kd = env.Kubectl.Context(env.Downstream)
	})

	JustBeforeEach(func() {
		out, err := k.Namespace(env.ClusterRegistrationNamespace).Apply(
			"-f", testenv.AssetPath("multi-cluster/gitreporestriction-namespace-selector.yaml"),
		)
		Expect(err).ToNot(HaveOccurred(), out)

		err = testenv.ApplyTemplate(
			k.Namespace(env.ClusterRegistrationNamespace),
			testenv.AssetPath("multi-cluster/gitreporestriction-ns-selector-gitrepo.yaml"),
			TemplateData{
				Name:                         gitrepoName,
				TargetNamespace:              namespace,
				ClusterRegistrationNamespace: env.ClusterRegistrationNamespace,
			},
		)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		out, err := k.Namespace(env.ClusterRegistrationNamespace).Delete(
			"gitrepo", gitrepoName, "--wait=false",
		)
		Expect(err).ToNot(HaveOccurred(), out)

		out, err = k.Namespace(env.ClusterRegistrationNamespace).Delete(
			"gitreporestriction", "ns-selector-restriction", "--wait=false",
		)
		Expect(err).ToNot(HaveOccurred(), out)

		_, _ = kd.Run("delete", "namespace", namespace, "--ignore-not-found")
	})

	When("the target namespace on the downstream cluster has the required label", func() {
		BeforeEach(func() {
			namespace = testenv.NewNamespaceName("ns-sel", r)
			gitrepoName = testenv.AddRandomSuffix("ns-sel", r)

			out, err := kd.Run("create", "namespace", namespace)
			Expect(err).ToNot(HaveOccurred(), out)

			out, err = kd.Label("namespace", namespace, "team=frontend")
			Expect(err).ToNot(HaveOccurred(), out)
		})

		It("deploys the workload to the target namespace", func() {
			Eventually(func() string {
				out, _ := kd.Namespace(namespace).Get("configmaps")
				return out
			}).Should(ContainSubstring("simple-config"))
		})
	})

	When("the target namespace exists on the downstream cluster but lacks the required label", func() {
		BeforeEach(func() {
			namespace = testenv.NewNamespaceName("ns-sel", r)
			gitrepoName = testenv.AddRandomSuffix("ns-sel", r)

			out, err := kd.Run("create", "namespace", namespace)
			Expect(err).ToNot(HaveOccurred(), out)
		})

		It("blocks deployment and reports an AllowedTargetNamespaceSelector error", func() {
			bundleLabel := "fleet.cattle.io/bundle-name=" + gitrepoName + "-simple"
			Eventually(func() string {
				out, _ := k.Get(
					"bundledeployments", "-A",
					"-l", bundleLabel,
					"-o", "jsonpath={.items[*].status.conditions}",
				)
				return out
			}).Should(ContainSubstring("AllowedTargetNamespaceSelector"))

			Consistently(func() string {
				out, _ := kd.Namespace(namespace).Get("configmaps")
				return out
			}, testenv.MediumTimeout, testenv.PollingInterval).ShouldNot(ContainSubstring("simple-config"))
		})
	})

	When("the target namespace does not exist on the downstream cluster", func() {
		BeforeEach(func() {
			namespace = testenv.NewNamespaceName("ns-sel", r)
			gitrepoName = testenv.AddRandomSuffix("ns-sel", r)
		})

		It("blocks deployment and reports a missing namespace error", func() {
			bundleLabel := "fleet.cattle.io/bundle-name=" + gitrepoName + "-simple"
			Eventually(func() string {
				out, _ := k.Get(
					"bundledeployments", "-A",
					"-l", bundleLabel,
					"-o", "jsonpath={.items[*].status.conditions}",
				)
				return out
			}).Should(ContainSubstring("does not exist on downstream cluster"))
		})
	})
})
