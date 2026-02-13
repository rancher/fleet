package singlecluster_test

import (
	"fmt"
	"path"
	"strconv"
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
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

			By("preserving secret types upon copy")
			Eventually(func(g Gomega) {
				s, err := k.Namespace(deployNamespace).Get("secret", "secret-image-pull", "-o=jsonpath='{.type}'")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(s).To(ContainSubstring("kubernetes.io/dockerconfigjson"))

			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

			By("deploying resources with templated values taken from cloned resources")
			Eventually(func(g Gomega) {
				cms, err := k.Namespace(deployNamespace).Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring(cmName))

				name, err := k.Namespace(deployNamespace).Get("configmaps", cmName, "-o", "jsonpath={.data.name}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(name).To(Equal("name-from-downstream-cluster-secret"))
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

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
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())
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

	Context("watching for changes to downstream resources", func() {
		BeforeEach(func() {
			asset = "single-cluster/helmop_downstream_resources.yaml"
			name = "helmop-downstream-copy-watch"
			deployNamespace = "helmop-downstream-copy-watch-ns"
			keepResources = false
			downstreamResources = nil
			valuesFrom = []fleet.ValuesFrom{
				{
					ConfigMapKeyRef: &fleet.ConfigMapKeySelector{
						Namespace:            env.Namespace,
						LocalObjectReference: fleet.LocalObjectReference{Name: "config-values"},
						Key:                  "values.yaml",
					},
				},
				{
					SecretKeyRef: &fleet.SecretKeySelector{
						Namespace:            env.Namespace,
						LocalObjectReference: fleet.LocalObjectReference{Name: "secret-values"},
						Key:                  "values.yaml",
					},
				},
			}
		})

		It("automatically re-deploys when a referenced configmap or secret changes", func() {
			By("copying resources initially")
			Eventually(func(g Gomega) {
				s, err := k.Namespace(deployNamespace).Get("secrets")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(s).To(ContainSubstring("secret-values"))

				cms, err := k.Namespace(deployNamespace).Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring("config-values"))
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

			By("verifying initial deployment with original values")
			Eventually(func(g Gomega) {
				cms, err := k.Namespace(deployNamespace).Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring(cmName))

				name, err := k.Namespace(deployNamespace).Get("configmaps", cmName, "-o", "jsonpath={.data.name}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(name).To(Equal("name-from-downstream-cluster-secret"))
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

			By("verifying the configmap has the initial value before updating")
			Eventually(func(g Gomega) {
				cm, err := k.Namespace(deployNamespace).Get("configmap", "config-values", "-o", "jsonpath={.data['values\\.yaml']}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cm).To(ContainSubstring("name-from-downstream-cluster-configmap"))
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

			clusterNameSpace := ""
			By("getting the cluster namespace for the downstream cluster")
			Eventually(func(g Gomega) {
				out, err := k.Namespace(env.Namespace).Get("clusters", "local", "-o", "jsonpath={.status.namespace}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(out).ToNot(BeEmpty())
				clusterNameSpace = out
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

			By("getting the initial DownstreamResourcesGeneration from the BundleDeployment")
			var initialGeneration int
			Eventually(func(g Gomega) {
				bd, err := k.Namespace(clusterNameSpace).Get("bundledeployment", "helmop-downstream-copy-watch", "-o", "jsonpath={.status.downstreamResourcesGeneration}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(bd).NotTo(BeEmpty())
				gen, err := strconv.Atoi(bd)
				g.Expect(err).ToNot(HaveOccurred())
				initialGeneration = gen
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

			By("updating the configmap")
			out, err := k.Patch("configmap", "config-values", "--type=merge", "-p", `{"data":{"values.yaml":"name: updated-configmap-value"}}`)
			Expect(err).ToNot(HaveOccurred(), out)

			By("verifying the configmap change propagates automatically")
			Eventually(func(g Gomega) {
				cm, err := k.Namespace(deployNamespace).Get("configmap", "config-values", "-o", "jsonpath={.data['values\\.yaml']}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cm).To(ContainSubstring("updated-configmap-value"))
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

			By("verifying the DownstreamResourcesGeneration is updated after configmap change")
			Eventually(func(g Gomega) {
				bd, err := k.Namespace(clusterNameSpace).Get("bundledeployment", "helmop-downstream-copy-watch", "-o", "jsonpath={.status.downstreamResourcesGeneration}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(bd).NotTo(BeEmpty())
				gen, err := strconv.Atoi(bd)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(gen).To(Equal(initialGeneration + 1))
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

			By("verifying the secret has the initial value before updating")
			Eventually(func(g Gomega) {
				s, err := k.Namespace(deployNamespace).Get("secret", "secret-values", "-o", "jsonpath={.data['values\\.yaml']}")
				g.Expect(err).ToNot(HaveOccurred())
				// The initial value should be base64 encoded "name: name-from-downstream-cluster-secret"
				g.Expect(s).To(Equal("bmFtZTogbmFtZS1mcm9tLWRvd25zdHJlYW0tY2x1c3Rlci1zZWNyZXQK"))
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

			By("getting the DownstreamResourcesGeneration after configmap update")
			var generationAfterConfigMapUpdate int
			Eventually(func(g Gomega) {
				bd, err := k.Namespace(clusterNameSpace).Get("bundledeployment", "helmop-downstream-copy-watch", "-o", "jsonpath={.status.downstreamResourcesGeneration}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(bd).NotTo(BeEmpty())
				gen, err := strconv.Atoi(bd)
				g.Expect(err).ToNot(HaveOccurred())
				generationAfterConfigMapUpdate = gen
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

			By("updating the secret data")
			out, err = k.Patch("secret", "secret-values", "--type=json", "-p", `[{"op": "replace", "path": "/data/values.yaml", "value": "bmFtZTogdXBkYXRlZC1zZWNyZXQtdmFsdWU="}]`)
			Expect(err).ToNot(HaveOccurred(), out)

			By("verifying the secret change propagates automatically")
			Eventually(func(g Gomega) {
				s, err := k.Namespace(deployNamespace).Get("secret", "secret-values", "-o", "jsonpath={.data['values\\.yaml']}")
				g.Expect(err).ToNot(HaveOccurred())
				// The value "bmFtZTogdXBkYXRlZC1zZWNyZXQtdmFsdWU=" is base64 encoded "name: updated-secret-value"
				g.Expect(s).To(Equal("bmFtZTogdXBkYXRlZC1zZWNyZXQtdmFsdWU="))
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

			By("verifying the DownstreamResourcesGeneration is updated after secret change")
			Eventually(func(g Gomega) {
				bd, err := k.Namespace(clusterNameSpace).Get("bundledeployment", "helmop-downstream-copy-watch", "-o", "jsonpath={.status.downstreamResourcesGeneration}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(bd).NotTo(BeEmpty())
				gen, err := strconv.Atoi(bd)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(gen).To(Equal(generationAfterConfigMapUpdate + 1))
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

			By("updating the secret-image-pull data")
			out, err = k.Patch("secret", "secret-image-pull", "--type=json", "-p", `[{"op": "add", "path": "/data/new-key", "value": "dXBkYXRlZC12YWx1ZQ=="}]`)
			Expect(err).ToNot(HaveOccurred(), out)

			By("verifying the secret data change propagates automatically")
			Eventually(func(g Gomega) {
				s, err := k.Namespace(deployNamespace).Get("secret", "secret-image-pull", "-o=jsonpath={.data.new-key}")
				g.Expect(err).ToNot(HaveOccurred())
				// The value "dXBkYXRlZC12YWx1ZQ==" is base64 encoded "updated-value"
				g.Expect(s).To(Equal("dXBkYXRlZC12YWx1ZQ=="))
			}).WithTimeout(30 * time.Second).WithPolling(testenv.LongPollingInterval).Should(Succeed())

			By("propagating the update to the deployment once the secret is updated")
			Eventually(func(g Gomega) {
				cms, err := k.Namespace(deployNamespace).Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring(cmName))

				name, err := k.Namespace(deployNamespace).Get("configmaps", cmName, "-o", "jsonpath={.data.name}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(name).To(Equal("updated-secret-value"))
			}).Should(Succeed())
		})

		AfterEach(func() {
			_, _ = k.Delete("helmop", name)
		})
	})

	Context("deploying with non-existent downstream resources", func() {
		BeforeEach(func() {
			asset = "single-cluster/helmop_downstream_resources.yaml"
			name = "helmop-downstream-nonexistent"
			deployNamespace = "helmop-downstream-nonexistent-ns"
			keepResources = false
			valuesFrom = []fleet.ValuesFrom{}
			// Reference resources that don't exist yet
			downstreamResources = []fleet.DownstreamResource{
				{
					Kind: "Secret",
					Name: "new-secret",
				},
				{
					Kind: "ConfigMap",
					Name: "new-configmap",
				},
			}
		})

		It("waits for resources to be created and then propagates them", func() {
			By("verifying the non-existent resources are not in the cluster")
			Consistently(func(g Gomega) {
				s, err := k.Namespace(deployNamespace).Get("secrets")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(s).NotTo(ContainSubstring("new-secret"))

				cms, err := k.Namespace(deployNamespace).Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).NotTo(ContainSubstring("new-configmap"))
			}).WithTimeout(10 * time.Second).WithPolling(2 * time.Second).Should(Succeed())

			By("creating the configmap")
			out, err := k.Create("configmap", "new-configmap", "--from-literal=data=configmap-value")
			Expect(err).ToNot(HaveOccurred(), out)

			By("creating the secret")
			out, err = k.Create("secret", "generic", "new-secret", "--from-literal=data=secret-value")
			Expect(err).ToNot(HaveOccurred(), out)

			By("verifying the newly created resources are now available")
			Eventually(func(g Gomega) {
				s, err := k.Namespace(deployNamespace).Get("secrets")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(s).To(ContainSubstring("new-secret"))

				cms, err := k.Namespace(deployNamespace).Get("configmaps")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cms).To(ContainSubstring("new-configmap"))
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

			By("verifying the data is correct in the resources")
			Eventually(func(g Gomega) {
				s, err := k.Namespace(deployNamespace).Get("secret", "new-secret", "-o", "jsonpath={.data.data}")
				g.Expect(err).ToNot(HaveOccurred())
				// "c2VjcmV0LXZhbHVl" is base64 encoded "secret-value"
				g.Expect(s).To(Equal("c2VjcmV0LXZhbHVl"))

				cm, err := k.Namespace(deployNamespace).Get("configmap", "new-configmap", "-o", "jsonpath={.data.data}")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cm).To(Equal("configmap-value"))
			}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())
		})

		AfterEach(func() {
			_, _ = k.Delete("helmop", name)
			_, _ = k.Delete("secret", "new-secret")
			_, _ = k.Delete("configmap", "new-configmap")
		})
	})
})
