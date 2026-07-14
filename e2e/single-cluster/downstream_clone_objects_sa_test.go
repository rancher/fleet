package singlecluster_test

import (
	"math/rand"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// This test verifies that cloning DownstreamResources is gated by the pinned
// service account's downstream RBAC, rather than the agent's cluster-admin
// credentials. The copy (and the later cleanup) run through an impersonating
// client, so a service account without write access must not be able to copy the
// resources, and granting the RBAC must let the copy converge and, on deletion,
// be cleaned up again under that same identity.
//
// The single-cluster (fleet-local) agent runs in cattle-fleet-local-system and
// resolves the pinned service account there, so that is where the SA lives and
// what the impersonation subject references.
var _ = Describe("Downstream objects cloning gated by service account RBAC", Ordered, func() {
	const agentNamespace = "cattle-fleet-local-system"

	var (
		k kubectl.Command
		r = rand.New(rand.NewSource(GinkgoRandomSeed()))

		name      string
		saName    string
		deployNS  string
		nsRole    string
		srcSecret string
		srcCM     string
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)

		id := testenv.NewNamespaceName("dsr-sa", r)
		name = id
		saName = id
		deployNS = id + "-ns"
		nsRole = id + "-nsrole"
		srcSecret = id + "-secret"
		srcCM = id + "-cm"

		// The pinned service account, resolved by the agent in its own namespace.
		// It starts with no RBAC, so the copy must be denied.
		out, err := k.Namespace(agentNamespace).Create("serviceaccount", saName)
		Expect(err).ToNot(HaveOccurred(), out)

		// Pre-create the deployment namespace so the test isolates the resource
		// write permission rather than the (cluster-scoped) namespace creation.
		out, err = k.Create("namespace", deployNS)
		Expect(err).ToNot(HaveOccurred(), out)

		// Source resources live in the fleet workspace namespace and are referenced
		// by DownstreamResources. The secret doubles as the chart's values source so
		// the Helm release installs once the copy is allowed.
		out, err = k.Create("secret", "generic", srcSecret, "--from-literal=values.yaml=name: dsr-value")
		Expect(err).ToNot(HaveOccurred(), out)

		out, err = k.Create("configmap", srcCM, "--from-literal=foo=bar")
		Expect(err).ToNot(HaveOccurred(), out)

		err = testenv.ApplyTemplate(k, testenv.AssetPath("single-cluster/helmop_downstream_resources_sa.yaml"), struct {
			Name                string
			Namespace           string
			ServiceAccount      string
			DeployNamespace     string
			Chart               string
			DownstreamResources []fleet.DownstreamResource
			ValuesFrom          []fleet.ValuesFrom
		}{
			Name:            name,
			Namespace:       env.Namespace,
			ServiceAccount:  saName,
			DeployNamespace: deployNS,
			Chart:           "https://github.com/rancher/fleet/raw/refs/heads/main/integrationtests/cli/assets/helmrepository/config-chart-0.1.0.tgz",
			DownstreamResources: []fleet.DownstreamResource{
				{Kind: "Secret", Name: srcSecret},
				{Kind: "ConfigMap", Name: srcCM},
			},
			ValuesFrom: []fleet.ValuesFrom{
				{
					SecretKeyRef: &fleet.SecretKeySelector{
						Namespace:            env.Namespace,
						LocalObjectReference: fleet.LocalObjectReference{Name: srcSecret},
						Key:                  "values.yaml",
					},
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		_, _ = k.Delete("helmop", name)
		_, _ = k.Delete("secret", srcSecret)
		_, _ = k.Delete("configmap", srcCM)
		_, _ = k.Namespace(agentNamespace).Delete("serviceaccount", saName)
		// Cluster-scoped RBAC is not removed with the namespace.
		_, _ = k.Delete("clusterrole", nsRole)
		_, _ = k.Delete("clusterrolebinding", nsRole)
		_, _ = k.Delete("namespace", deployNS, "--wait=false")
	})

	It("blocks the copy without RBAC and lets it converge once granted", func() {
		By("recording the denial as a forbidden error on the bundle deployment")
		var clusterNS string
		Eventually(func(g Gomega) {
			ns, err := k.Get("clusters", "local", "-o", "jsonpath={.status.namespace}")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(ns).ToNot(BeEmpty())
			clusterNS = ns
		}).WithTimeout(testenv.MediumTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

		// The forbidden detail lives on the BundleDeployment (the HelmOp only
		// reports that it is waiting), and it must name the pinned service account:
		// the write was attempted as that identity, not the agent's cluster-admin.
		Eventually(func(g Gomega) {
			msg, err := k.Namespace(clusterNS).Get("bundledeployment", name, "-o", "jsonpath={.status.conditions[?(@.type=='Ready')].message}")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(msg).To(And(ContainSubstring("forbidden"), ContainSubstring(saName)))
		}).WithTimeout(testenv.MediumTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

		By("granting the service account access to the deployment namespace")
		out, err := k.Namespace(deployNS).Create(
			"role", "dsr-copy",
			"--verb=get,list,watch,create,update,patch,delete",
			"--resource=secrets,configmaps",
		)
		Expect(err).ToNot(HaveOccurred(), out)
		out, err = k.Namespace(deployNS).Create(
			"rolebinding", "dsr-copy",
			"--role=dsr-copy",
			"--serviceaccount="+agentNamespace+":"+saName,
		)
		Expect(err).ToNot(HaveOccurred(), out)

		// The deploy path (namespace label SSA + Helm install) also runs as the SA,
		// so it needs to reach the (cluster-scoped) deployment namespace for the
		// release to install and the cleanup-on-delete to have a release to act on.
		out, err = k.Create(
			"clusterrole", nsRole,
			"--verb=get,list,watch,patch,update",
			"--resource=namespaces",
		)
		Expect(err).ToNot(HaveOccurred(), out)
		out, err = k.Create(
			"clusterrolebinding", nsRole,
			"--clusterrole="+nsRole,
			"--serviceaccount="+agentNamespace+":"+saName,
		)
		Expect(err).ToNot(HaveOccurred(), out)

		By("copying the resources and installing the release once the RBAC is granted")
		Eventually(func(g Gomega) {
			secrets, err := k.Namespace(deployNS).Get("secrets")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(secrets).To(ContainSubstring(srcSecret))

			cms, err := k.Namespace(deployNS).Get("configmaps")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(cms).To(ContainSubstring(srcCM))

			// Wait for the release to be installed, so the delete below has a
			// release to clean the copies up from.
			ready, err := k.Get("helmop", name, "-o", "jsonpath={.status.display.readyBundleDeployments}")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(ready).To(Equal("1/1"))
		}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())

		By("cleaning up the copied resources under the same service account when the HelmOp is deleted")
		out, err = k.Delete("helmop", name)
		Expect(err).ToNot(HaveOccurred(), out)

		Eventually(func(g Gomega) {
			secrets, err := k.Namespace(deployNS).Get("secrets")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(secrets).ToNot(ContainSubstring(srcSecret))

			cms, err := k.Namespace(deployNS).Get("configmaps")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(cms).ToNot(ContainSubstring(srcCM))
		}).WithTimeout(testenv.LongTimeout).WithPolling(testenv.LongPollingInterval).Should(Succeed())
	})
})
