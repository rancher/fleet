package multicluster_test

import (
	"fmt"
	"path"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

type DownstreamResource struct {
	Kind string
	Name string
}

// This test uses two clusters to demonstrate cloning of configured resources to downstream clusters.
var _ = Describe("Downstream objects cloning", Ordered, func() {
	var (
		k  kubectl.Command
		kd kubectl.Command

		asset         string
		name          string
		keepResources bool
		valuesFrom    []fleet.ValuesFrom
		cmName        = "test-simple-chart-config"
	)

	BeforeEach(func() {
		k = env.Kubectl.Context(env.Upstream)
		kd = env.Kubectl.Context(env.Downstream).Namespace("fleet-default")
	})

	JustBeforeEach(func() {
		cmAsset := path.Join(testenv.AssetPath("multi-cluster/values-cm.yaml"))
		out, err := k.Namespace(env.ClusterRegistrationNamespace).Create("configmap", "config-values", fmt.Sprintf("--from-file=values.yaml=%s", cmAsset))
		Expect(err).ToNot(HaveOccurred(), out)

		DeferCleanup(func() {
			_, _ = kd.Delete("secret", "secret-values")
			_, _ = kd.Delete("configmaps", "config-values")
		})

		secretAsset := path.Join(testenv.AssetPath("multi-cluster/values-secret.yaml"))
		out, err = k.Namespace(env.ClusterRegistrationNamespace).Create("secret", "generic", "secret-values", fmt.Sprintf("--from-file=values.yaml=%s", secretAsset))
		Expect(err).ToNot(HaveOccurred(), out)

		// Not actually used by the deployment, but validates secret type preservation over copy to downstream cluster
		out, err = k.Namespace(env.ClusterRegistrationNamespace).Create(
			"secret",
			"docker-registry",
			"secret-image-pull",
			// credentials do not matter, we just need the combination of fields to obtain a valid secret
			"--docker-username=test-user",
			"--docker-password=test-pwd",
		)
		Expect(err).ToNot(HaveOccurred(), out)

		err = testenv.ApplyTemplate(k.Namespace(env.ClusterRegistrationNamespace), testenv.AssetPath(asset), struct {
			Name                  string
			Namespace             string
			Repo                  string
			Chart                 string
			Version               string
			PollingInterval       time.Duration
			HelmSecretName        string
			InsecureSkipTLSVerify bool
			DownstreamResources   []DownstreamResource
			KeepResources         bool
			ValuesFrom            []fleet.ValuesFrom
		}{
			name,
			env.ClusterRegistrationNamespace,
			"",
			"https://github.com/rancher/fleet/raw/refs/heads/main/integrationtests/cli/assets/helmrepository/config-chart-0.1.0.tgz",
			"",
			0,
			"",
			false,
			[]DownstreamResource{
				{
					Kind: "Secret",
					Name: "secret-values",
				},
				{
					Kind: "ConfigMap",
					Name: "config-values",
				},
				{
					Kind: "Secret",
					Name: "secret-image-pull",
				},
			},
			keepResources,
			valuesFrom,
		})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		_, _ = k.Namespace(env.ClusterRegistrationNamespace).Delete("secret", "secret-values")
		_, _ = k.Namespace(env.ClusterRegistrationNamespace).Delete("secret", "secret-image-pull")
		_, _ = k.Namespace(env.ClusterRegistrationNamespace).Delete("configmap", "config-values")
	})

	Context("with configured resources for cloning downstream", func() {
		BeforeEach(func() {
			asset = "multi-cluster/helmop_downstream_resources.yaml"
			name = "helmop-downstream-copy"
			keepResources = false
			valuesFrom = []fleet.ValuesFrom{
				{
					SecretKeyRef: &fleet.SecretKeySelector{
						Namespace:            env.ClusterRegistrationNamespace,
						LocalObjectReference: fleet.LocalObjectReference{Name: "secret-values"},
						Key:                  "values.yaml",
					},
				},
			}
		})

		It("deploys the HelmOp and clones resources downstream", func() {
			By("copying resources downstream")
			Eventually(func(g Gomega) {
				s, err := kd.Get("secrets")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(s).To(ContainSubstring("secret-values"))

				cms, err := kd.Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring("config-values"))
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

			By("preserving secret types upon copy to the downstream cluster")
			Eventually(func(g Gomega) {
				s, err := kd.Get("secret", "secret-image-pull", "-o=jsonpath='{.type}'")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(s).To(ContainSubstring("kubernetes.io/dockerconfigjson"))

			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

			By("deploying resources with templated values taken from cloned resources")
			Eventually(func(g Gomega) {
				cms, err := kd.Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring(cmName))

				name, err := kd.Get("configmaps", cmName, "-o", "jsonpath={.data.name}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(name).To(Equal("name-from-downstream-cluster-secret"))
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

			By("deleting cloned resources when the bundle deployment is deleted")
			out, err := k.Namespace(env.ClusterRegistrationNamespace).Delete("helmop", name)
			Expect(err).ToNot(HaveOccurred(), out)

			Eventually(func(g Gomega) {
				s, err := kd.Get("secrets")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(s).NotTo(ContainSubstring("secret-values"))

				cms, err := kd.Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).NotTo(ContainSubstring("config-values"))
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())
		})
	})

	Context("updating a copied resource at its source", func() {
		BeforeEach(func() {
			asset = "multi-cluster/helmop_downstream_resources.yaml"
			name = "helmop-downstream-copy-update"
			keepResources = false
			valuesFrom = []fleet.ValuesFrom{
				{
					ConfigMapKeyRef: &fleet.ConfigMapKeySelector{
						Namespace:            env.ClusterRegistrationNamespace,
						LocalObjectReference: fleet.LocalObjectReference{Name: "config-values"},
						Key:                  "values.yaml",
					},
				},
			}
		})

		It("updates the deployment when a copied resource is updated at its source and the bundle is next reconciled", func() {
			By("deploying resources with templated values taken from cloned resources")
			Eventually(func(g Gomega) {
				cms, err := kd.Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring(cmName))

				name, err := kd.Get("configmaps", cmName, "-o", "jsonpath={.data.name}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(name).To(Equal("name-from-downstream-cluster-configmap"))
			}).Should(Succeed())

			By("updating a copied resource upstream")
			out, err := k.Namespace(env.ClusterRegistrationNamespace).Patch("configmap", "config-values", "--type=merge", "-p", `{"data":{"values.yaml":"name: new-name"}}`)
			Expect(err).ToNot(HaveOccurred(), out)

			By("reconciling the helmop")
			// remove the secret as downstream resource; does not have any effect on the
			// bundle deployment itself, but should trigger a new reconcile of the helmop, hence the bundle.
			out, err = k.Namespace(env.ClusterRegistrationNamespace).Patch("helmop", name, "--type=json", "-p", `[{"op": "remove", "path": "/spec/downstreamResources/0"}]`)
			Expect(err).ToNot(HaveOccurred(), out)

			By("propagating the update to the deployment once the helmop is reconciled")
			Eventually(func(g Gomega) {
				cms, err := kd.Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring(cmName))

				name, err := kd.Get("configmaps", cmName, "-o", "jsonpath={.data.name}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(name).To(Equal("new-name"))
			}).Should(Succeed())
		})

		AfterEach(func() {
			_, _ = k.Namespace(env.ClusterRegistrationNamespace).Delete("helmop", name)
		})
	})

	Context("with configured resources for cloning downstream and keepresources is true", func() {
		BeforeEach(func() {
			asset = "multi-cluster/helmop_downstream_resources.yaml"
			name = "helmop-downstream-copy-keepresources"
			keepResources = true
			valuesFrom = []fleet.ValuesFrom{
				{
					ConfigMapKeyRef: &fleet.ConfigMapKeySelector{
						Namespace:            env.ClusterRegistrationNamespace,
						LocalObjectReference: fleet.LocalObjectReference{Name: "config-values"},
						Key:                  "values.yaml",
					},
				},
			}
		})

		It("leaves the cloned resources in the downstream cluster", func() {
			cmName := "test-simple-chart-config"

			By("copying resources downstream")
			Eventually(func(g Gomega) {
				s, err := kd.Get("secrets")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(s).To(ContainSubstring("secret-values"))

				cms, err := kd.Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring("config-values"))
			}).Should(Succeed())

			By("deploying resources with templated values taken from cloned resources")
			Eventually(func(g Gomega) {
				cms, err := kd.Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring(cmName))

				name, err := kd.Get("configmaps", cmName, "-o", "jsonpath={.data.name}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(name).To(Equal("name-from-downstream-cluster-configmap"))
			}).Should(Succeed())

			By("keeping cloned resources when the bundle deployment is deleted")
			out, err := k.Namespace(env.ClusterRegistrationNamespace).Delete("helmop", name)
			Expect(err).ToNot(HaveOccurred(), out)

			Eventually(func(g Gomega) {
				s, err := kd.Get("secrets")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(s).To(ContainSubstring("secret-values"))

				cms, err := kd.Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring("config-values"))
			}).Should(Succeed())
		})
	})
})
