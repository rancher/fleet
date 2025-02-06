package singlecluster_test

import (
	"fmt"
	"math/rand"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

var shards = []string{"shard0", "shard1", "shard2"}

var _ = Describe("Filtering events by shard", Label("sharding"), func() {
	var (
		k               kubectl.Command
		gitrepoName     string
		r               = rand.New(rand.NewSource(GinkgoRandomSeed()))
		targetNamespace string
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		targetNamespace = testenv.NewNamespaceName("target", r)
		gitrepoName = testenv.RandomFilename("sharding-test", r)

	})

	for _, shard := range shards {
		When(fmt.Sprintf("deploying a gitrepo labeled with shard ID %s", shard), func() {
			JustBeforeEach(func() {
				err := testenv.ApplyTemplate(
					k,
					testenv.AssetPath("gitrepo/gitrepo_sharded.yaml"),
					struct {
						Name            string
						Repo            string
						Branch          string
						PollingInterval string
						TargetNamespace string
						ShardID         string
					}{
						gitrepoName,
						"https://github.com/rancher/fleet-test-data",
						"master",
						"15s",           // default
						targetNamespace, // to avoid conflicts with other tests
						shard,
					},
				)
				Expect(err).ToNot(HaveOccurred())
			})

			It(fmt.Sprintf("deploys the gitrepo via the gitjob labeled with shard ID %s", shard), func() {
				shardNodeSelector, err := k.Namespace("cattle-fleet-system").Get(
					"deploy",
					fmt.Sprintf("fleet-controller-shard-%s", shard),
					"-o=jsonpath={.spec.template.spec.nodeSelector}",
				)
				Expect(err).ToNot(HaveOccurred())

				By("checking the gitjob pod has the same nodeSelector as the sharded controller deployment")
				Eventually(func(g Gomega) {
					pods, err := k.Namespace("fleet-local").Get(
						"pods",
						"-o",
						`jsonpath={range .items[*]}{.metadata.name}{"\t"}{.spec.nodeSelector}{"\n"}{end}`,
					)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(pods).ToNot(BeEmpty(), "no pod in namespace fleet-local")

					var podNodeSelector string
					for _, pod := range strings.Split(pods, "\n") {
						fields := strings.Split(pod, "\t")
						podName := fields[0]
						if strings.HasPrefix(podName, "sharding-test") {
							podNodeSelector = fields[1]
							break
						}
					}

					g.Expect(podNodeSelector).ToNot(BeEmpty(), "sharding-test* pod not found or has empty node selector")
					g.Expect(podNodeSelector).To(Equal(shardNodeSelector))
				}).Should(Succeed())

				By("checking the configmap exists")
				Eventually(func() string {
					out, _ := k.Namespace(targetNamespace).Get("configmaps")
					return out
				}).Should(ContainSubstring("test-simple-chart-config"))

				By("checking the bundle bears the shard label with the right shard ID")
				bundleName := fmt.Sprintf("%s-simple-chart", gitrepoName)
				Eventually(func(g Gomega) {
					shardLabelValue, err := k.Get(
						"bundle",
						bundleName,
						`-o jsonpath='{.metadata.labels.fleet\.cattle\.io/shard-ref}'`,
					)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(shardLabelValue).To(Equal(shard))
				}).Should(Succeed())

				By("checking the bundle deployment bears the shard label with the right shard ID")
				clusterNS, err := k.Get(
					"cluster.fleet.cattle.io",
					"local",
					"-n",
					"fleet-local",
					`-o=jsonpath='{.status.namespace}'`,
				)
				Expect(err).ToNot(HaveOccurred())

				Eventually(func(g Gomega) {
					shardLabelValue, err := k.Get(
						"bundledeployment",
						bundleName,
						"-n",
						clusterNS,
						`-o=jsonpath='{.metadata.labels.fleet\.cattle\.io/shard-ref}'`,
					)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(shardLabelValue).To(Equal(shard))
				}).Should(Succeed())
			})

			AfterEach(func() {
				_, _ = k.Delete("gitrepo", gitrepoName)
				_, _ = k.Delete("ns", targetNamespace, "--wait=false")
			})
		})
	}

	When("deploying a gitrepo labeled with an unknown shard ID", func() {
		JustBeforeEach(func() {
			err := testenv.ApplyTemplate(k, testenv.AssetPath("gitrepo/gitrepo_sharded.yaml"), struct {
				Name            string
				Repo            string
				Branch          string
				PollingInterval string
				TargetNamespace string
				ShardID         string
			}{
				gitrepoName,
				"https://github.com/rancher/fleet-test-data",
				"master",
				"15s",           // default
				targetNamespace, // to avoid conflicts with other tests
				"unknown",
			})
			Expect(err).ToNot(HaveOccurred())
		})

		It("does not deploy the gitrepo", func() {
			By("checking the configmap does not exist")
			Consistently(func() string {
				out, _ := k.Namespace(targetNamespace).Get("configmaps")
				return out
			}).ShouldNot(ContainSubstring("test-simple-chart-config"))
		})

		AfterEach(func() {
			_, _ = k.Delete("gitrepo", gitrepoName)
			_, _ = k.Delete("ns", targetNamespace, "--wait=false")
		})
	})
})
