package singlecluster_test

import (
	"slices"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	yaml "sigs.k8s.io/yaml"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Helm Chart Values", func() {
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

	When("helm chart validates inputs", func() {
		BeforeEach(func() {
			asset = "single-cluster/helm-verify.yaml"
		})

		It("replaces cluster label in values and valuesFiles", func() {
			Eventually(func() string {
				out, _ := k.Namespace("default").Get("configmaps")

				return out
			}).Should(ContainSubstring("app-config"))
			out, _ := k.Namespace("default").Get("configmap", "app-config", "-o", "jsonpath={.data}")
			Expect(out).Should(SatisfyAll(
				ContainSubstring(`"name":"local"`),
				ContainSubstring(`"url":"local"`),
			))

			By("checking apply configs are not in the resources", func() {
				out, err := k.Get("bundles", "helm-verify-test-helm-verify", "-o", "jsonpath={.spec}")
				Expect(err).ToNot(HaveOccurred(), out)

				var b fleetv1.BundleSpec
				err = yaml.Unmarshal([]byte(out), &b)
				Expect(err).ToNot(HaveOccurred())

				names := slices.Collect(func(yield func(string) bool) {
					for _, r := range b.Resources {
						if !yield(r.Name) {
							return
						}
					}
				})
				Expect(names).NotTo(ContainElement("fleet.yaml"))
				Expect(names).NotTo(ContainElement("values.yaml"))
			})
		})
	})

	When("fleet.yaml has templated values", func() {
		BeforeEach(func() {
			asset = "single-cluster/helm-cluster-values.yaml"
		})

		It("generates values and applies them", func() {
			Eventually(func() string {
				out, _ := k.Namespace("default").Get("configmaps")

				return out
			}).Should(ContainSubstring("test-template-values"))
			out, _ := k.Namespace("default").Get("configmap", "test-template-values", "-o", "jsonpath={.data}")

			var t map[string]interface{}
			err := yaml.Unmarshal([]byte(out), &t)
			Expect(err).ToNot(HaveOccurred())
			Expect(t).To(HaveKeyWithValue("name", "name-local"))
			Expect(t).To(HaveKeyWithValue("namespace", ContainSubstring("fleet-local")))
			Expect(t).To(HaveKey("annotations"))
			Expect(t["annotations"]).To(Equal(`{"app":"fleet","more":"data"}`))
			Expect(t).To(HaveKeyWithValue("image", "rancher/mirrored-library-busybox:1.34.1"))
			Expect(t).To(HaveKeyWithValue("imagePullPolicy", "IfNotPresent"))
			Expect(t).To(HaveKeyWithValue("clusterValues", "{}"))
			Expect(t).To(HaveKeyWithValue("global", "null"))

			Eventually(func() string {
				out, _ := k.Namespace("default").Get("deployments")

				return out
			}).Should(ContainSubstring("test-template-values"))
			out, _ = k.Namespace("default").Get("deployments", "test-template-values", "-ojson")
			d := appsv1.Deployment{}
			err = yaml.Unmarshal([]byte(out), &d)
			Expect(err).ToNot(HaveOccurred())

			Expect(*d.Spec.Replicas).To(Equal(int32(1)))
			Expect(d.Spec.Template.Spec.Containers[0].Image).To(Equal("rancher/mirrored-library-busybox:1.34.1"))
			Expect(d.Spec.Template.Spec.Containers[0].ImagePullPolicy).To(Equal(corev1.PullIfNotPresent))
			Expect(d.Spec.Template.Annotations).To(SatisfyAll(
				HaveKeyWithValue("app", "fleet"),
				HaveKeyWithValue("more", "data"),
			))
			Expect(d.Spec.Template.Labels).To(HaveKeyWithValue("name", "local"))
			Expect(d.Spec.Template.Labels).To(HaveKeyWithValue("policy", ""))
			Expect(d.Spec.Template.Spec.HostAliases[0].Hostnames).To(ContainElements("one", "two", "three"))
		})
	})
})
