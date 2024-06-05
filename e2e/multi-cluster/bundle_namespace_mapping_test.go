package multicluster_test

import (
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

var _ = Describe("Bundle Namespace Mapping", Label("difficult"), func() {
	var (
		k  kubectl.Command
		kd kubectl.Command

		asset     string
		namespace string
		data      any

		interval = 2 * time.Second
		duration = 30 * time.Second

		r = rand.New(rand.NewSource(GinkgoRandomSeed()))
	)

	type TemplateData struct {
		ClusterNamespace    string
		ProjectNamespace    string
		TargetNamespace     string
		BundleSelectorLabel string
		Restricted          bool
	}

	BeforeEach(func() {
		k = env.Kubectl.Context(env.Upstream)
		kd = env.Kubectl.Context(env.Downstream)
	})

	JustBeforeEach(func() {
		err := testenv.ApplyTemplate(k.Namespace(namespace), testenv.AssetPath(asset), data)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		out, err := k.Delete("ns", namespace)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	Context("with bundlenamespacemapping", func() {
		When("bundle selector does not match", func() {
			BeforeEach(func() {
				namespace = testenv.NewNamespaceName("bnm-nomatch", r)
				asset = "multi-cluster/bundle-namespace-mapping.yaml"
				data = TemplateData{env.ClusterRegistrationNamespace, namespace, "", "mismatch", false}
			})

			It("does not deploy to the mapped downstream cluster", func() {
				Consistently(func() string {
					out, _ := k.Get("bundledeployments", "-A", "-l", "fleet.cattle.io/bundle-namespace="+namespace)
					return out
				}, duration, interval).ShouldNot(ContainSubstring("simpleapp-bundle-diffs"))
			})
		})
	})

	// the cluster resource in not in the same namespace as the gitrepo
	// resource, a BundleNamespaceMapping is needed
	Context("with bundlenamespacemapping and gitreporestriction", func() {
		When("targetNamespace is included in allow list", func() {
			BeforeEach(func() {
				namespace = "project1"
				asset = "multi-cluster/bundle-namespace-mapping.yaml"
				data = TemplateData{env.ClusterRegistrationNamespace, namespace, "targetNamespace: project1simpleapp", "one", true}
			})

			It("deploys to the mapped downstream cluster", func() {
				Eventually(func() string {
					out, _ := k.Get("bundledeployments", "-A", "-l", "fleet.cattle.io/bundle-namespace="+namespace)
					return out
				}).Should(ContainSubstring("simpleapp-bundle-diffs"))
				Eventually(func() string {
					out, _ := kd.Namespace("project1simpleapp").Get("configmaps")
					return out
				}).Should(ContainSubstring("app-config"))
			})
		})

		When("downstream namespace is not included in allow list", func() {
			BeforeEach(func() {
				namespace = "project2"
				asset = "multi-cluster/bundle-namespace-mapping.yaml"
				data = TemplateData{env.ClusterRegistrationNamespace, namespace, "targetNamespace: denythisnamespace", "one", true}
			})

			It("denies deployment to downstream cluster", func() {
				Eventually(func() string {
					out, _ := k.Namespace(namespace).Get("gitrepo", "simpleapp",
						"-o=jsonpath={.status.conditions[*].message}",
					)
					return out
				}).Should(ContainSubstring("disallowed targetNamespace denythisnamespace"))
			})
		})

		When("target namespace is empty", func() {
			BeforeEach(func() {
				namespace = "project3"
				asset = "multi-cluster/bundle-namespace-mapping.yaml"
				data = TemplateData{env.ClusterRegistrationNamespace, namespace, "", "one", true}
			})

			It("denies deployment to downstream cluster", func() {
				Eventually(func() string {
					out, _ := k.Namespace(namespace).Get("gitrepo", "simpleapp",
						"-o=jsonpath={.status.conditions[*].message}",
					)
					return out
				}).Should(ContainSubstring("empty targetNamespace denied"))
			})
		})
	})
})
