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

var _ = Describe("Bundle Depends On", func() {
	var (
		k kubectl.Command

		namespace string
		required  string
		dependsOn string

		interval = 2 * time.Second
		duration = 30 * time.Second

		r = rand.New(rand.NewSource(GinkgoRandomSeed()))
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

	AfterEach(func() {
		if namespace == "" {
			return
		}

		out, err := k.Delete("ns", namespace)
		Expect(err).ToNot(HaveOccurred(), out)

		out, err = k.Namespace(env.Namespace).Delete("bundle", dependsOn)
		Expect(err).ToNot(HaveOccurred(), out)
		out, err = k.Namespace(env.Namespace).Delete("bundle", required)
		Expect(err).ToNot(HaveOccurred(), out)
	})

	When("bundle depends on a bundle in the same namespace", func() {
		BeforeEach(func() {
			required = "required"
			dependsOn = "depends-on"
			namespace = testenv.NewNamespaceName("bnm-nomatch", r)
		})

		JustBeforeEach(func() {
			err := testenv.ApplyTemplate(k.Namespace(env.Namespace), testenv.AssetPath("multi-cluster/bundle-depends-on.yaml"),
				TemplateData{dependsOn, env.Namespace, namespace})
			Expect(err).ToNot(HaveOccurred())
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

			By("deploying the required bundle")
			err := testenv.ApplyTemplate(k.Namespace(env.Namespace), testenv.AssetPath("multi-cluster/bundle-cm.yaml"),
				TemplateData{required, env.Namespace, namespace})
			Expect(err).ToNot(HaveOccurred())

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

	When("bundle depends on a bundle with acceptedStates including Modified", func() {
		BeforeEach(func() {
			required = "required-modified"
			dependsOn = "depends-on-accepted-states"
			namespace = testenv.NewNamespaceName("bnm-accepted", r)
		})

		It("allows dependent bundle to deploy when dependency is in Modified state", func() {
			By("deploying the required bundle first")
			err := testenv.ApplyTemplate(k.Namespace(env.Namespace), testenv.AssetPath("multi-cluster/bundle-cm-modified.yaml"),
				TemplateData{required, env.Namespace, namespace})
			Expect(err).ToNot(HaveOccurred())

			By("waiting for required bundle to be ready")
			Eventually(func() string {
				out, err := k.Namespace(env.Namespace).Get("bundle", required, "-o=jsonpath={.status.display}")
				if err != nil {
					return ""
				}
				var d fleet.BundleDisplay
				_ = json.Unmarshal([]byte(out), &d)
				return d.ReadyClusters
			}, 2*time.Minute, interval).Should(Equal("1/1"))

			By("waiting for ConfigMap to be deployed")
			Eventually(func() error {
				_, err := k.Namespace(namespace).Get("configmap", "root-will-be-modified")
				return err
			}, 1*time.Minute, interval).Should(Succeed())

			By("modifying the deployed ConfigMap to trigger drift")
			_, err = k.Namespace(namespace).Run(
				"patch", "configmap", "root-will-be-modified",
				"--type=merge", "-p", `{"data":{"value":"modified-externally"}}`,
			)
			Expect(err).ToNot(HaveOccurred())

			By("waiting for required bundle to enter Modified state")
			Eventually(func() string {
				out, err := k.Namespace(env.Namespace).Get("bundle", required, "-o=jsonpath={.status.display.state}")
				if err != nil {
					return ""
				}
				return out
			}, 2*time.Minute, interval).Should(Equal("Modified"))

			By("deploying the dependent bundle that accepts Modified state")
			err = testenv.ApplyTemplate(k.Namespace(env.Namespace), testenv.AssetPath("multi-cluster/bundle-depends-on-accepted-states.yaml"),
				TemplateData{dependsOn, env.Namespace, namespace})
			Expect(err).ToNot(HaveOccurred())

			By("verifying dependent bundle becomes ready despite dependency being Modified")
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
