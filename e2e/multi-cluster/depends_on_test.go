package multicluster_test

import (
	"encoding/json"
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("Bundle Depends On", Label("difficult"), func() {
	var (
		k kubectl.Command

		asset     string
		namespace string
		data      any
		required  string
		dependsOn string

		interval = 2 * time.Second
		duration = 30 * time.Second
	)

	type TemplateData struct {
		Name                         string
		ClusterRegistrationNamespace string
		ProjectNamespace             string
	}

	BeforeEach(func() {
		k = env.Kubectl.Context(env.Upstream)
		Expect(env.Namespace).To(Equal("fleet-local"))
	})

	JustBeforeEach(func() {
		err := testenv.ApplyTemplate(k.Namespace(env.Namespace), testenv.AssetPath(asset), data)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		out, err := k.Delete("ns", namespace)
		Expect(err).ToNot(HaveOccurred(), out)

		out, err = k.Namespace(env.Namespace).Delete("bundle", dependsOn)
		Expect(err).ToNot(HaveOccurred(), out)

		out, err = k.Namespace(env.Namespace).Delete("bundle", required)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	deployRequiredBundle := func() {
		err := testenv.ApplyTemplate(k.Namespace(env.Namespace), testenv.AssetPath("multi-cluster/bundle-cm.yaml"),
			TemplateData{required, env.Namespace, namespace})
		Expect(err).ToNot(HaveOccurred())
	}

	When("bundle depends on a bundle in the same namespace", func() {
		BeforeEach(func() {
			required = "required"
			dependsOn = "depends-on"
			namespace = testenv.NewNamespaceName("bnm-nomatch", rand.New(rand.NewSource(GinkgoRandomSeed())))
			asset = "multi-cluster/bundle-depends-on.yaml"
			data = TemplateData{dependsOn, env.Namespace, namespace}
		})

		It("shows an error until dependency is fulfilled", func() {
			By("waiting for bundle to error")
			Eventually(func() []fleet.NonReadyResource {
				out, err := k.Namespace(env.Namespace).Get("bundle", dependsOn, "-o=jsonpath={.status.summary}")
				if err != nil {
					return []fleet.NonReadyResource{}
				}
				var sum fleet.BundleSummary
				_ = json.Unmarshal([]byte(out), &sum)
				return sum.NonReadyResources
			}, duration, interval).Should(ContainElement(fleet.NonReadyResource{
				State:   "ErrApplied",
				Message: "list bundledeployments: no bundles matching labels role=root in namespace fleet-local",
				Name:    "fleet-local/local",
			}))

			By("deploying the required bundle", deployRequiredBundle)

			By("waiting for bundle to ready")
			Eventually(func() string {
				out, err := k.Namespace(env.Namespace).Get("bundle", dependsOn, "-o=jsonpath={.status.display}")
				if err != nil {
					return ""
				}
				var d fleet.BundleDisplay
				_ = json.Unmarshal([]byte(out), &d)
				return d.ReadyClusters
			}, 5*time.Minute, interval).Should(Equal("1/1"))
		})
	})
})
