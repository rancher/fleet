package multicluster_test

import (
	"math/rand"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

// This test uses labels in clusters to demonstrate that applying or
// deleting the label installs or uninstalls the bundle deployment
var _ = Describe("Target clusters by label", func() {
	var (
		k  kubectl.Command
		kd kubectl.Command

		asset     string
		namespace string
		data      any

		r = rand.New(rand.NewSource(GinkgoRandomSeed()))
	)

	type TemplateData struct {
		ProjectNamespace string
		TargetNamespace  string
	}

	BeforeEach(func() {
		k = env.Kubectl.Context(env.Upstream)
		kd = env.Kubectl.Context(env.ManagedDownstream)
	})

	JustBeforeEach(func() {
		err := testenv.ApplyTemplate(k.Namespace(env.ClusterRegistrationNamespace), testenv.AssetPath(asset), data)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		out, err := k.Namespace(env.ClusterRegistrationNamespace).Delete("gitrepo", "simpleapplabels", "--wait=false")
		Expect(err).ToNot(HaveOccurred(), out)
	})

	Context("if cluster has the expected label, bundle is deployed", func() {
		BeforeEach(func() {
			namespace = testenv.NewNamespaceName("label-not-set", r)
			asset = "multi-cluster/bundle-deployment-labels.yaml"
			data = TemplateData{env.ClusterRegistrationNamespace, namespace}
			// set the expected label
			out, err := k.Namespace(env.ClusterRegistrationNamespace).Label("cluster.fleet.cattle.io", dsCluster, "envlabels=test")
			Expect(err).ToNot(HaveOccurred(), out)
		})

		It("deploys to the mapped downstream cluster and when label is deleted it removes the deployment", func() {
			Eventually(func() string {
				out, _ := k.Get("bundledeployments", "-A", "-l", "fleet.cattle.io/bundle-name=simpleapplabels-simple")
				return out
			}).Should(ContainSubstring("simpleapplabels-simple"))
			Eventually(func() string {
				out, _ := kd.Get("configmaps", "-A")
				return out
			}).Should(ContainSubstring("simple-config"))

			// delete the label (bundledeployment should be deleted and resources in cluster deleted)
			out, err := k.Namespace(env.ClusterRegistrationNamespace).Label("cluster.fleet.cattle.io", dsCluster, "envlabels-")
			Expect(err).ToNot(HaveOccurred(), out)

			Eventually(func() string {
				out, _ := k.Get("bundledeployments", "-A", "-l", "fleet.cattle.io/bundle-name=simpleapplabels-simple")
				return out
			}).ShouldNot(ContainSubstring("simpleapplabels-simple"))
			Eventually(func() string {
				out, _ := kd.Namespace(namespace).Get("configmaps")
				return out
			}).ShouldNot(ContainSubstring("simple-config"))

			// re-apply the label (bundledeployment should be created and applied again)
			out, err = k.Namespace(env.ClusterRegistrationNamespace).Label("cluster.fleet.cattle.io", dsCluster, "envlabels=test")
			Expect(err).ToNot(HaveOccurred(), out)

			Eventually(func() string {
				out, _ := k.Get("bundledeployments", "-A", "-l", "fleet.cattle.io/bundle-name=simpleapplabels-simple")
				return out
			}).Should(ContainSubstring("simpleapplabels-simple"))
			Eventually(func() string {
				out, _ := kd.Namespace(namespace).Get("configmaps")
				return out
			}).Should(ContainSubstring("simple-config"))
		})
	})
})
