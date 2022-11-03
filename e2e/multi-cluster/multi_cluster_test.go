package mc_examples_test

import (
	"encoding/json"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	appsv1 "k8s.io/api/apps/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These tests use the examples from https://github.com/rancher/fleet-examples/tree/master/multi-cluster
var _ = Describe("Multi Cluster Examples", func() {
	var (
		asset string
		k     kubectl.Command
		kd    kubectl.Command
	)

	helmVersion := func(repo string) (string, error) {
		out, err := k.Get("bundledeployments", "-A", "-l", "fleet.cattle.io/repo-name="+repo, "-o=jsonpath={.items[*].spec.options.helm.version}")
		if err != nil {
			return "", err
		}

		return out, nil
	}

	helmRepo := func(repo string) (string, error) {
		out, err := k.Get("bundledeployments", "-A", "-l", "fleet.cattle.io/repo-name="+repo, "-o=jsonpath={.items[*].spec.options.helm.repo}")
		if err != nil {
			return "", err
		}

		return out, nil
	}

	BeforeEach(func() {
		k = env.Kubectl.Context(env.Fleet).Namespace(env.Namespace)
		kd = env.Kubectl.Context(env.Downstream)
	})

	JustBeforeEach(func() {
		out, err := k.Apply("-f", testenv.AssetPath(asset))
		Expect(err).ToNot(HaveOccurred(), out)
	})

	AfterEach(func() {
		out, err := k.Delete("-f", testenv.AssetPath(asset))
		Expect(err).ToNot(HaveOccurred(), out)

	})

	When("creating gitrepo resource", func() {
		Context("containing a helm chart", func() {
			BeforeEach(func() {
				asset = "multi-cluster/helm.yaml"
			})

			It("deploys the helm chart in the downstream cluster", func() {
				Eventually(func() string {
					out, _ := kd.Namespace("fleet-mc-helm-example").Get("pods")
					return out
				}).Should(SatisfyAll(ContainSubstring("frontend-"), ContainSubstring("redis-")))
			})
		})

		Context("containing a manifest", func() {
			BeforeEach(func() {
				asset = "multi-cluster/manifests.yaml"
			})

			It("deploys the manifest", func() {
				Eventually(func() string {
					out, _ := kd.Namespace("fleet-mc-manifest-example").Get("pods")
					return out
				}).Should(SatisfyAll(ContainSubstring("frontend-"), ContainSubstring("redis-")))

				out, err := kd.Namespace("fleet-mc-manifest-example").Get("deployment", "-o", "json", "frontend")
				Expect(err).ToNot(HaveOccurred())

				d := &appsv1.Deployment{}
				err = json.Unmarshal([]byte(out), d)
				Expect(err).ToNot(HaveOccurred())
				Expect(*d.Spec.Replicas).To(Equal(int32(3)))
			})
		})

		Context("containing a kustomize manifest", func() {
			BeforeEach(func() {
				asset = "multi-cluster/kustomize.yaml"
			})

			It("runs kustomize and deploys the manifest", func() {
				Eventually(func() string {
					out, _ := kd.Namespace("fleet-mc-kustomize-example").Get("pods")
					return out
				}).Should(ContainSubstring("frontend-"))
			})
		})

		Context("containing an kustomized helm chart", func() {
			BeforeEach(func() {
				asset = "multi-cluster/helm-kustomize.yaml"
			})

			It("deploys the helm chart", func() {
				Eventually(func() string {
					out, _ := kd.Namespace("fleet-mc-helm-kustomize-example").Get("pods")
					return out
				}).Should(ContainSubstring("frontend-"))
			})
		})

		Context("containing an external helm chart", func() {
			BeforeEach(func() {
				asset = "multi-cluster/helm-external.yaml"
			})

			It("deploys the helm chart", func() {
				Eventually(func() string {
					out, _ := kd.Namespace("fleet-mc-helm-external-example").Get("pods")
					return out
				}).Should(ContainSubstring("frontend-"))
			})
		})

		Context("containing an external helm chart with targetCustomizations", func() {
			BeforeEach(func() {
				asset = "multi-cluster/helm-target-customizations.yaml"
			})

			It("can replace the chart version and url", func() {
				expectedVersion := "0.0.36"

				// Verify bundledeployment changes
				Eventually(func() string {
					out, _ := helmVersion("helm-target-customizations")
					return out
				}).Should(ContainSubstring(expectedVersion))

				Eventually(func() string {
					out, _ := helmRepo("helm-target-customizations")
					return out
				}).Should(ContainSubstring("https://charts.truecharts.org///"))

				// Verify actual deployment downstream
				Eventually(func() string {
					out, _ := kd.Get("deployments", "-A", "-l", "helm.sh/chart=radicale-"+expectedVersion)
					return out
				}).Should(ContainSubstring("radicale"))
			})
		})
	})
})
