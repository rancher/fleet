package singlecluster_test

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

// This test uses a single cluster to demonstrate cloning of configured resources within the same cluster.
var _ = Describe("Downstream objects cloning", Ordered, func() {
	var (
		k kubectl.Command

		asset               string
		name                string
		deployNamespace     string
		keepResources       bool
		valuesFrom          []fleet.ValuesFrom
		downstreamResources []fleet.DownstreamResource
		cmName              = "test-simple-chart-config"
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
	})

	JustBeforeEach(func() {
		cmAsset := path.Join(testenv.AssetPath("single-cluster/values-cm.yaml"))
		out, err := k.Create("configmap", "config-values", fmt.Sprintf("--from-file=values.yaml=%s", cmAsset))
		Expect(err).ToNot(HaveOccurred(), out)

		secretAsset := path.Join(testenv.AssetPath("single-cluster/values-secret.yaml"))
		out, err = k.Create("secret", "generic", "secret-values", fmt.Sprintf("--from-file=values.yaml=%s", secretAsset))
		Expect(err).ToNot(HaveOccurred(), out)

		// Not actually used by the deployment, but validates secret type preservation over copy
		out, err = k.Create(
			"secret",
			"docker-registry",
			"secret-image-pull",
			// credentials do not matter, we just need the combination of fields to obtain a valid secret
			"--docker-username=test-user",
			"--docker-password=test-pwd",
		)
		Expect(err).ToNot(HaveOccurred(), out)

		// Use default downstream resources if not set
		if downstreamResources == nil {
			downstreamResources = []fleet.DownstreamResource{
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
			}
		}

		err = testenv.ApplyTemplate(k, testenv.AssetPath(asset), struct {
			Name                  string
			Namespace             string
			DeployNamespace       string
			Repo                  string
			Chart                 string
			Version               string
			PollingInterval       time.Duration
			HelmSecretName        string
			InsecureSkipTLSVerify bool
			DownstreamResources   []fleet.DownstreamResource
			KeepResources         bool
			ValuesFrom            []fleet.ValuesFrom
		}{
			name,
			env.Namespace,
			deployNamespace,
			"",
			"https://github.com/rancher/fleet/raw/refs/heads/main/integrationtests/cli/assets/helmrepository/config-chart-0.1.0.tgz",
			"",
			0,
			"",
			false,
			downstreamResources,
			keepResources,
			valuesFrom,
		})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		_, _ = k.Delete("secret", "secret-values")
		_, _ = k.Delete("secret", "secret-image-pull")
		_, _ = k.Delete("configmap", "config-values")
	})

	Context("with configured resources for cloning downstream", func() {
		BeforeEach(func() {
			asset = "single-cluster/helmop_downstream_resources.yaml"
			name = "helmop-downstream-copy"
			deployNamespace = "helmop-downstream-copy-ns"
			keepResources = false
			downstreamResources = nil
			valuesFrom = []fleet.ValuesFrom{
				{
					SecretKeyRef: &fleet.SecretKeySelector{
						Namespace:            env.Namespace,
						LocalObjectReference: fleet.LocalObjectReference{Name: "secret-values"},
						Key:                  "values.yaml",
					},
				},
			}
		})

		It("deploys the HelmOp and clones resources", func() {
			By("copying resources")
			Eventually(func(g Gomega) {
				s, err := k.Namespace(deployNamespace).Get("secrets")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(s).To(ContainSubstring("secret-values"))

				cms, err := k.Namespace(deployNamespace).Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring("config-values"))
			}).Should(Succeed())

			By("preserving secret types upon copy")
			Eventually(func(g Gomega) {
				s, err := k.Namespace(deployNamespace).Get("secret", "secret-image-pull", "-o=jsonpath='{.type}'")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(s).To(ContainSubstring("kubernetes.io/dockerconfigjson"))

			}).Should(Succeed())

			By("deploying resources with templated values taken from cloned resources")
			Eventually(func(g Gomega) {
				cms, err := k.Namespace(deployNamespace).Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring(cmName))

				name, err := k.Namespace(deployNamespace).Get("configmaps", cmName, "-o", "jsonpath={.data.name}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(name).To(Equal("name-from-downstream-cluster-secret"))
			}).Should(Succeed())

			By("deleting cloned resources when the bundle deployment is deleted")
			out, err := k.Delete("helmop", name)
			Expect(err).ToNot(HaveOccurred(), out)

			Eventually(func(g Gomega) {
				cms, err := k.Namespace(deployNamespace).Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).NotTo(ContainSubstring(cmName))

				cms, err = k.Namespace(deployNamespace).Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).NotTo(ContainSubstring("config-values"))

				secrets, err := k.Namespace(deployNamespace).Get("secrets")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(secrets).NotTo(ContainSubstring("secret-values"))
			}).Should(Succeed())
		})
	})

	Context("with configured resources for cloning downstream and keepresources is true", func() {
		BeforeEach(func() {
			asset = "single-cluster/helmop_downstream_resources.yaml"
			name = "helmop-downstream-copy-keepresources"
			deployNamespace = "helmop-downstream-copy-keepresources-ns"
			keepResources = true
			downstreamResources = nil
			valuesFrom = []fleet.ValuesFrom{
				{
					ConfigMapKeyRef: &fleet.ConfigMapKeySelector{
						Namespace:            env.Namespace,
						LocalObjectReference: fleet.LocalObjectReference{Name: "config-values"},
						Key:                  "values.yaml",
					},
				},
			}
		})

		It("leaves the cloned resources in the cluster", func() {
			cmName := "test-simple-chart-config"

			By("copying resources")
			Eventually(func(g Gomega) {
				s, err := k.Namespace(deployNamespace).Get("secrets")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(s).To(ContainSubstring("secret-values"))

				cms, err := k.Namespace(deployNamespace).Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring("config-values"))
			}).Should(Succeed())

			By("deploying resources with templated values taken from cloned resources")
			Eventually(func(g Gomega) {
				cms, err := k.Namespace(deployNamespace).Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring(cmName))

				name, err := k.Namespace(deployNamespace).Get("configmaps", cmName, "-o", "jsonpath={.data.name}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(name).To(Equal("name-from-downstream-cluster-configmap"))
			}).Should(Succeed())

			By("keeping cloned resources when the bundle deployment is deleted")
			out, err := k.Delete("helmop", name)
			Expect(err).ToNot(HaveOccurred(), out)

			Eventually(func(g Gomega) {
				cms, err := k.Namespace(deployNamespace).Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring(cmName))

				cms, err = k.Namespace(deployNamespace).Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring("config-values"))

				secrets, err := k.Namespace(deployNamespace).Get("secrets")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(secrets).To(ContainSubstring("secret-values"))
			}).Should(Succeed())
		})
	})
})
