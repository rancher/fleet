package agentmanagement_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/agent"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	referenceNotAcceptedMessage = "needs label " + fleet.KubeConfigReferenceableFromLabel
	managementTargetMessage     = "targets the management cluster"

	// importTokenPrefix mirrors config.ImportTokenPrefix, which is unexported
	// on this release branch.
	importTokenPrefix = "import-token-"
)

// The envtest API server stands in for the management cluster: a kubeconfig
// built from the suite's rest.Config resolves to the same kube-system
// namespace UID the controller sees through its own client.
var _ = Describe("Cluster import kubeconfig secrets", func() {
	var (
		clusterNS string
		secretNS  string
	)

	BeforeEach(func() {
		// Import requires a valid agentTLSMode before it connects anywhere.
		previous := config.Get()
		withTLSMode := *previous
		withTLSMode.AgentTLSMode = config.AgentTLSModeStrict
		config.Set(&withTLSMode)
		DeferCleanup(func() { config.Set(previous) })

		for _, target := range []*string{&clusterNS, &secretNS} {
			ns := newGeneratedNamespace("cluster-import-")
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())
			*target = ns.Name
			DeferCleanup(deleteNamespace, ns.Name)
		}
	})

	// setConfig replaces the global config for the current spec.
	setConfig := func(mutate func(*config.Config)) {
		previous := config.Get()
		updated := *previous
		mutate(&updated)
		config.Set(&updated)
		DeferCleanup(func() { config.Set(previous) })
	}

	createKubeconfigSecret := func(namespace string) *corev1.Secret {
		GinkgoHelper()
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      "management-kubeconfig",
			},
			Data: map[string][]byte{
				config.KubeConfigSecretValueKey: utils.FromEnvTestConfig(cfg),
				config.APIServerURLKey:          []byte(cfg.Host),
				config.APIServerCAKey:           cfg.CAData,
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, secret))).To(Succeed())
		})
		return secret
	}

	createCluster := func(namespace, name, agentNamespace string, secret *corev1.Secret) *fleet.Cluster {
		GinkgoHelper()
		cluster := newCluster(namespace, name)
		cluster.Spec.KubeConfigSecret = secret.Name
		cluster.Spec.AgentNamespace = agentNamespace
		if secret.Namespace != namespace {
			cluster.Spec.KubeConfigSecretNamespace = secret.Namespace
		}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, cluster))).To(Succeed())
			objectGone(cluster).Should(Succeed())
			deleteNamespace(clusterNamespaceName(namespace, name))
		})
		return cluster
	}

	// importedCondition returns the Cluster's current Imported condition.
	importedCondition := func(g Gomega, cluster *fleet.Cluster) genericcondition.GenericCondition {
		c := &fleet.Cluster{}
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), c)).To(Succeed())
		var cond genericcondition.GenericCondition
		g.Expect(c.Status.Conditions).To(ContainElement(HaveField("Type", "Imported"), &cond))
		return cond
	}

	expectImportedFalse := func(cluster *fleet.Cluster, message string) {
		GinkgoHelper()
		Eventually(func(g Gomega) {
			cond := importedCondition(g, cluster)
			g.Expect(cond.Status).To(Equal(corev1.ConditionFalse))
			g.Expect(cond.Message).To(ContainSubstring(message))
		}).Should(Succeed())
	}

	importToken := func(cluster *fleet.Cluster) *fleet.ClusterRegistrationToken {
		return &fleet.ClusterRegistrationToken{ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      names.SafeConcatName(importTokenPrefix + cluster.Name),
		}}
	}

	// agentAbsent asserts that the import never got past the management
	// cluster check: neither the agent namespace nor the import token exist.
	agentAbsent := func(cluster *fleet.Cluster, agentNamespace string) {
		GinkgoHelper()
		Consistently(func(g Gomega) {
			objectAbsent(newNamespace(agentNamespace))(g)
			objectAbsent(importToken(cluster))(g)
		}).Should(Succeed())
	}

	It("does not deploy an agent onto the management cluster for a Cluster in a workspace namespace", func() {
		secret := createKubeconfigSecret(clusterNS)
		cluster := createCluster(clusterNS, "same-namespace", "cluster-import-agent-a", secret)

		expectImportedFalse(cluster, managementTargetMessage)
		agentAbsent(cluster, "cluster-import-agent-a")
	})

	It("uses a kubeconfig secret in another namespace only while it names the Cluster's namespace", func() {
		secret := createKubeconfigSecret(secretNS)
		cluster := createCluster(clusterNS, "cross-namespace", "cluster-import-agent-b", secret)

		expectImportedFalse(cluster, referenceNotAcceptedMessage)

		By("labeling the secret for another namespace")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(secret), secret)).To(Succeed())
			secret.Labels = map[string]string{fleet.KubeConfigReferenceableFromLabel: secretNS}
			g.Expect(k8sClient.Update(ctx, secret)).To(Succeed())
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(importedCondition(g, cluster).Message).To(ContainSubstring(referenceNotAcceptedMessage))
		}).Should(Succeed())

		By("labeling the secret for the Cluster's namespace")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(secret), secret)).To(Succeed())
			secret.Labels = map[string]string{fleet.KubeConfigReferenceableFromLabel: clusterNS}
			g.Expect(k8sClient.Update(ctx, secret)).To(Succeed())
		}).Should(Succeed())
		// The reference is accepted now; the management cluster target is
		// still not.
		expectImportedFalse(cluster, managementTargetMessage)

		By("removing the label")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(secret), secret)).To(Succeed())
			secret.Labels = nil
			g.Expect(k8sClient.Update(ctx, secret)).To(Succeed())
		}).Should(Succeed())
		expectImportedFalse(cluster, referenceNotAcceptedMessage)
		agentAbsent(cluster, "cluster-import-agent-b")
	})

	It("keeps the management cluster target unaccepted when unlabeled well-known namespaces are allowed", func() {
		setConfig(func(c *config.Config) { c.AllowUnlabeledKubeConfigSecretNamespaces = true })

		err := k8sClient.Create(ctx, newNamespace("fleet-default"))
		if !apierrors.IsAlreadyExists(err) {
			Expect(err).NotTo(HaveOccurred())
		}
		secret := createKubeconfigSecret("fleet-default")
		cluster := createCluster(clusterNS, "allow-unlabeled", "cluster-import-agent-c", secret)

		expectImportedFalse(cluster, managementTargetMessage)
		agentAbsent(cluster, "cluster-import-agent-c")
	})

	It("reports a reference that is not accepted for an already deployed agent, without removing the agent", func() {
		const agentNamespace = "cluster-import-agent-d"
		Expect(k8sClient.Create(ctx, newNamespace(agentNamespace))).To(Succeed())
		DeferCleanup(deleteNamespace, agentNamespace)
		sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: agentNamespace, Name: agent.DefaultName}}
		Expect(k8sClient.Create(ctx, sa)).To(Succeed())
		// envtest runs no namespace controller, so deleting the namespace
		// would leave the service account behind.
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, sa))).To(Succeed())
		})

		secret := createKubeconfigSecret(secretNS)
		cluster := newCluster(clusterNS, "deployed")
		cluster.Spec.ClientID = "deployed-client-id"
		cluster.Spec.KubeConfigSecret = secret.Name
		cluster.Spec.KubeConfigSecretNamespace = secret.Namespace
		cluster.Spec.AgentNamespace = agentNamespace
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, cluster))).To(Succeed())
			objectGone(cluster).Should(Succeed())
			deleteNamespace(clusterNamespaceName(clusterNS, "deployed"))
		})

		// Mark the agent as deployed, as an earlier release would have.
		Eventually(func(g Gomega) {
			c := &fleet.Cluster{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), c)).To(Succeed())
			c.Status.AgentMigrated = true
			c.Status.CattleNamespaceMigrated = true
			c.Status.AgentNamespaceMigrated = true
			c.Status.AgentDeployedGeneration = new(int64)
			c.Status.Agent.Namespace = agentNamespace
			g.Expect(k8sClient.Status().Update(ctx, c)).To(Succeed())
		}).Should(Succeed())

		expectImportedFalse(cluster, referenceNotAcceptedMessage)
		Consistently(func(g Gomega) {
			c := &fleet.Cluster{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), c)).To(Succeed())
			g.Expect(c.Status.AgentDeployedGeneration).NotTo(BeNil())
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sa), &corev1.ServiceAccount{})).To(Succeed())
		}).Should(Succeed())
	})

	It("proceeds with the import for a Cluster in the bootstrap namespace", func() {
		setConfig(func(c *config.Config) { c.Bootstrap.Namespace = clusterNS })

		secret := createKubeconfigSecret(clusterNS)
		cluster := createCluster(clusterNS, "bootstrap-namespace", "cluster-import-agent-e", secret)

		// The import token is created right after the management cluster
		// check; the rest of the deployment needs a token controller, which
		// envtest does not run.
		objectExists(importToken(cluster)).Should(Succeed())
		Consistently(func(g Gomega) {
			g.Expect(importedCondition(g, cluster).Message).NotTo(ContainSubstring(managementTargetMessage))
		}).Should(Succeed())
	})
})
