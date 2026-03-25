package multicluster_test

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
)

// This test uses two clusters to demonstrate offline cluster handling.
var _ = Describe("Offline cluster detection", func() {
	var (
		k     kubectl.Command
		kd    kubectl.Command
		asset string
		name  string
	)

	BeforeEach(func() {
		k = env.Kubectl.Context(env.Upstream)
		kd = env.Kubectl.Context(env.Downstream).Namespace("fleet-default")
	})

	Context("cluster with deployed workload becomes offline", func() {
		BeforeEach(func() {
			asset = "multi-cluster/helmop.yaml"
			name = "test-offline-cluster"

			err := testenv.ApplyTemplate(k.Namespace(env.ClusterRegistrationNamespace), testenv.AssetPath(asset), struct {
				Name                  string
				Namespace             string
				Repo                  string
				Chart                 string
				Version               string
				PollingInterval       time.Duration
				HelmSecretName        string
				InsecureSkipTLSVerify bool
			}{
				name,
				env.ClusterRegistrationNamespace,
				"",
				"https://github.com/rancher/fleet/raw/refs/heads/main/integrationtests/cli/assets/helmrepository/config-chart-0.1.0.tgz",
				"",
				0,
				"",
				false,
			})
			Expect(err).ToNot(HaveOccurred())

			DeferCleanup(func() {
				out, err := kd.Namespace("cattle-fleet-system").Run("scale", "deployment", "fleet-agent", "--replicas=1", "--timeout=60s")
				Expect(err).ToNot(HaveOccurred(), out)

				_, _ = k.Namespace(env.ClusterRegistrationNamespace).Delete("helmop", name)
			})
		})
		It("marks any offline cluster as such, along with its bundle deployments", func() {
			By("checking the initial online state of the cluster")
			// Cluster should be ready
			Eventually(func(g Gomega) {
				out, err := k.Get(
					"cluster", "-n", env.ClusterRegistrationNamespace,
					"-o", `jsonpath={.items[0].status.conditions}`,
				)
				g.Expect(err).ToNot(HaveOccurred(), out)

				checkReadyCondition(g, out, "", "True")
			}).To(Succeed())

			// Bundle deployment should be ready
			Eventually(func(g Gomega) {
				out, err := k.Get(
					"bundledeployments", "-A",
					"-l", "fleet.cattle.io/bundle-name="+name,
					"-l", "fleet.cattle.io/bundle-namespace="+env.ClusterRegistrationNamespace,
					"-o", `jsonpath={.items[0].status.conditions}`,
				)
				g.Expect(err).ToNot(HaveOccurred(), out)

				checkReadyCondition(g, out, "", "True")
			}).To(Succeed())

			By("taking the cluster offline")
			out, err := kd.Namespace("cattle-fleet-system").Run("scale", "deployment", "fleet-agent", "--replicas=0", "--timeout=60s")
			Expect(err).ToNot(HaveOccurred(), out)

			By("checking that the bundle deployment and the cluster appear offline")
			// Cluster should be offline
			Eventually(func(g Gomega) {
				out, err := k.Get(
					"cluster", "-n", env.ClusterRegistrationNamespace,
					"-o", `jsonpath={.items[0].status.conditions}`,
				)
				g.Expect(err).ToNot(HaveOccurred(), out)

				checkReadyCondition(g, out, "cluster is offline", "Unknown")
			}).To(Succeed())

			// Bundle deployment should be offline
			Eventually(func(g Gomega) {
				out, err := k.Get(
					"bundledeployments", "-A",
					"-l", "fleet.cattle.io/bundle-name="+name,
					"-l", "fleet.cattle.io/bundle-namespace="+env.ClusterRegistrationNamespace,
					"-o", `jsonpath={.items[0].status.conditions}`,
				)
				g.Expect(err).ToNot(HaveOccurred(), out)

				checkReadyCondition(g, out, "cluster is offline", "Unknown")
			}).To(Succeed())

			By("taking the cluster back online")
			out, err = kd.Namespace("cattle-fleet-system").Run("scale", "deployment", "fleet-agent", "--replicas=1", "--timeout=60s")
			Expect(err).ToNot(HaveOccurred(), out)

			By("checking the new online state of the cluster")
			// Cluster should be ready again
			Eventually(func(g Gomega) {
				out, err := k.Get(
					"cluster", "-n", env.ClusterRegistrationNamespace,
					"-o", `jsonpath={.items[0].status.conditions}`,
				)
				g.Expect(err).ToNot(HaveOccurred(), out)

				checkReadyCondition(g, out, "", "True")
			}).To(Succeed())

			// Bundle deployment should be ready again
			Eventually(func(g Gomega) {
				out, err := k.Get(
					"bundledeployments", "-A",
					"-l", "fleet.cattle.io/bundle-name="+name,
					"-l", "fleet.cattle.io/bundle-namespace="+env.ClusterRegistrationNamespace,
					"-o", `jsonpath={.items[0].status.conditions}`,
				)
				g.Expect(err).ToNot(HaveOccurred(), out)

				checkReadyCondition(g, out, "", "True")
			}).To(Succeed())
		})
	})
})

func checkReadyCondition(g Gomega, out, msg, status string) {
	conds := []genericcondition.GenericCondition{}
	err := json.Unmarshal([]byte(out), &conds)
	g.Expect(err).ToNot(HaveOccurred())

	var readyCond *genericcondition.GenericCondition
	for _, c := range conds {
		if c.Type == "Ready" {
			readyCond = &c
		}
	}

	g.Expect(readyCond).NotTo(BeNil())
	g.Expect(readyCond.Message).To(ContainSubstring(msg))
	g.Expect(readyCond.Status).To(Equal(corev1.ConditionStatus(status)))
}
