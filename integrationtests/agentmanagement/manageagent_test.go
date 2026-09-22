package agentmanagement_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("agent bundle", func() {
	var namespace string

	BeforeEach(func() {
		ns := newGeneratedNamespace("manage-agent-test-")
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		namespace = ns.Name
	})

	It("deploys the agent of a cluster as a bundle in the cluster's namespace", func() {
		Expect(k8sClient.Create(ctx, newCluster(namespace, "simple"))).To(Succeed())

		agentBundleExists(namespace, "simple").Should(Succeed())

		bundle := agentBundle(namespace, "simple")
		Expect(bundle.Spec.DefaultNamespace).To(Equal(systemNamespace))
		Expect(bundle.Spec.Helm).NotTo(BeNil())
		Expect(bundle.Spec.Helm.TakeOwnership).To(BeTrue())

		Expect(bundle.Labels).To(HaveKeyWithValue(fleet.InternalBundleLabel, "true"))

		// The bundle is deployed to that one cluster only, and never to a
		// cluster that opted out of the managed agent.
		Expect(bundle.Spec.Targets).To(HaveLen(1))
		Expect(bundle.Spec.Targets[0].ClusterName).To(Equal("simple"))
		Expect(bundle.Spec.Targets[0].ClusterSelector.MatchExpressions).To(ConsistOf(
			metav1.LabelSelectorRequirement{
				Key:      "fleet.cattle.io/non-managed-agent",
				Operator: metav1.LabelSelectorOpDoesNotExist,
			},
		))
	})

	It("carries the full agent manifest, scoped to the system namespace", func() {
		Expect(k8sClient.Create(ctx, newCluster(namespace, "manifest"))).To(Succeed())

		agentBundleExists(namespace, "manifest").Should(Succeed())

		bundle := agentBundle(namespace, "manifest")
		Expect(agentManifestKinds(bundle)).To(ConsistOf(
			"ClusterRole//"+systemNamespace+"-fleet-agent-role",
			"ClusterRoleBinding//"+systemNamespace+"-fleet-agent-role-binding",
			"ServiceAccount/"+systemNamespace+"/fleet-agent",
			"ServiceAccount/"+systemNamespace+"/default",
			"Deployment/"+systemNamespace+"/fleet-agent",
			"NetworkPolicy/"+systemNamespace+"/default-allow-all",
		))

		deployment := agentDeployment(bundle)
		Expect(deployment.Spec.Template.Spec.ServiceAccountName).To(Equal("fleet-agent"))
		Expect(deployment.Spec.Replicas).To(HaveValue(BeEquivalentTo(1)))
		Expect(agentContainer(bundle).Image).To(Equal(config.DefaultAgentImage))

		// The default agent namespace gets no AGENT_SCOPE
		Expect(agentContainer(bundle).Env).To(ContainElement(corev1.EnvVar{Name: "AGENT_SCOPE", Value: ""}))
	})

	It("scopes the agent to a cluster's custom agent namespace", func() {
		cluster := newCluster(namespace, "scoped")
		cluster.Spec.AgentNamespace = "custom-agent-namespace"
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		agentBundleExists(namespace, "scoped").Should(Succeed())

		bundle := agentBundle(namespace, "scoped")
		Expect(bundle.Spec.DefaultNamespace).To(Equal("custom-agent-namespace"))
		Expect(agentDeployment(bundle).Namespace).To(Equal("custom-agent-namespace"))
		Expect(agentContainer(bundle).Env).To(ContainElement(
			corev1.EnvVar{Name: "AGENT_SCOPE", Value: "custom-agent-namespace"},
		))
	})

	It("maintains one bundle per cluster in the namespace, without touching the others", func() {
		Expect(k8sClient.Create(ctx, newCluster(namespace, "first"))).To(Succeed())
		agentBundleExists(namespace, "first").Should(Succeed())
		firstVersion := agentBundle(namespace, "first").ResourceVersion

		Expect(k8sClient.Create(ctx, newCluster(namespace, "second"))).To(Succeed())
		agentBundleExists(namespace, "second").Should(Succeed())

		consistently(func(g Gomega) {
			g.Expect(agentBundle(namespace, "first").ResourceVersion).To(Equal(firstVersion))
		}).Should(Succeed())
	})

	It("leaves the bundle untouched while the cluster is unchanged", func() {
		Expect(k8sClient.Create(ctx, newCluster(namespace, "stable"))).To(Succeed())
		agentBundleExists(namespace, "stable").Should(Succeed())
		resourceVersion := agentBundle(namespace, "stable").ResourceVersion

		// Repeated reconciles of the namespace must converge on the same
		// bundle rather than rewriting it.
		touchNamespace(namespace, "once")
		touchNamespace(namespace, "twice")

		// Every reconcile of the namespace rebuilds the bundles of all its
		// clusters, so the bundle of a cluster added afterwards proves that
		// the stable bundle has been through at least one more reconcile.
		Expect(k8sClient.Create(ctx, newCluster(namespace, "witness"))).To(Succeed())
		agentBundleExists(namespace, "witness").Should(Succeed())

		consistently(func(g Gomega) {
			g.Expect(agentBundle(namespace, "stable").ResourceVersion).To(Equal(resourceVersion))
		}).Should(Succeed())
	})

	It("deploys no agent to a cluster managed by another cluster manager", func() {
		managed := newCluster(namespace, "externally-managed")
		managed.Labels = map[string]string{fleet.ClusterManagementLabel: "other-manager"}
		Expect(k8sClient.Create(ctx, managed)).To(Succeed())
		Expect(k8sClient.Create(ctx, newCluster(namespace, "fleet-managed"))).To(Succeed())

		// The bundle of the cluster Fleet does manage proves the namespace has
		// been reconciled, so the missing bundle is a decision, not a delay.
		agentBundleExists(namespace, "fleet-managed").Should(Succeed())

		consistently(objectAbsent(&fleet.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      agentBundleName("externally-managed"),
			},
		})).Should(Succeed())
	})

	It("keeps deploying the agent bundle to labelled clusters, leaving opt-outs to targeting", func() {
		labelled := newCluster(namespace, "labelled")
		labelled.Labels = map[string]string{"env": "prod"}
		Expect(k8sClient.Create(ctx, labelled)).To(Succeed())

		// A cluster that opted out of the managed agent still gets a bundle;
		// the bundle's own target selector is what keeps the agent away from
		// it.
		optedOut := newCluster(namespace, "opted-out")
		optedOut.Labels = map[string]string{"fleet.cattle.io/non-managed-agent": "true"}
		Expect(k8sClient.Create(ctx, optedOut)).To(Succeed())

		agentBundleExists(namespace, "labelled").Should(Succeed())
		agentBundleExists(namespace, "opted-out").Should(Succeed())

		targets := agentBundle(namespace, "opted-out").Spec.Targets
		Expect(targets).To(HaveLen(1))
		Expect(targets[0].ClusterName).To(Equal("opted-out"))
		Expect(targets[0].ClusterSelector.MatchExpressions).To(ConsistOf(
			metav1.LabelSelectorRequirement{
				Key:      "fleet.cattle.io/non-managed-agent",
				Operator: metav1.LabelSelectorOpDoesNotExist,
			},
		))
	})

	It("removes the bundle of a deleted cluster from the namespace", func() {
		Expect(k8sClient.Create(ctx, newCluster(namespace, "departing"))).To(Succeed())
		Expect(k8sClient.Create(ctx, newCluster(namespace, "remaining"))).To(Succeed())
		agentBundleExists(namespace, "departing").Should(Succeed())
		agentBundleExists(namespace, "remaining").Should(Succeed())
		remainingVersion := agentBundle(namespace, "remaining").ResourceVersion

		Expect(k8sClient.Delete(ctx, newCluster(namespace, "departing"))).To(Succeed())
		objectGone(newCluster(namespace, "departing")).Should(Succeed())
		touchNamespace(namespace, "cluster-removed")

		objectGone(&fleet.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      agentBundleName("departing"),
			},
		}).Should(Succeed())

		// Only the orphan goes; the remaining cluster keeps its agent.
		Expect(agentBundle(namespace, "remaining").ResourceVersion).To(Equal(remainingVersion))
	})

	It("removes the agent of a cluster that is being deleted", func() {
		terminating := newCluster(namespace, "terminating")
		terminating.Finalizers = []string{"fleet.cattle.io/integration-test"}
		Expect(k8sClient.Create(ctx, terminating)).To(Succeed())
		DeferCleanup(func() {
			updateCluster(namespace, "terminating", func(cluster *fleet.Cluster) {
				cluster.Finalizers = nil
			})
		})
		agentBundleExists(namespace, "terminating").Should(Succeed())

		// The finalizer keeps the cluster around after the delete, so the
		// cluster is still listed in its namespace while it goes away.
		Expect(k8sClient.Delete(ctx, terminating)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(getCluster(g, namespace, "terminating").DeletionTimestamp.IsZero()).To(BeFalse())
		}).Should(Succeed())

		touchNamespace(namespace, "cluster-terminating")

		objectGone(&fleet.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      agentBundleName("terminating"),
			},
		}).Should(Succeed())
	})

	Describe("agent settings taken from the cluster", func() {
		It("passes the cluster's agent environment variables to the agent and records their hash", func() {
			Expect(k8sClient.Create(ctx, newCluster(namespace, "env"))).To(Succeed())
			agentBundleExists(namespace, "env").Should(Succeed())

			updateCluster(namespace, "env", func(cluster *fleet.Cluster) {
				cluster.Spec.AgentEnvVars = []corev1.EnvVar{{Name: "HTTPS_PROXY", Value: "http://proxy:3128"}}
			})

			Eventually(func(g Gomega) {
				g.Expect(agentContainer(agentBundle(namespace, "env")).Env).To(ContainElement(
					corev1.EnvVar{Name: "HTTPS_PROXY", Value: "http://proxy:3128"},
				))
			}).Should(Succeed())

			var hash string
			Eventually(func(g Gomega) {
				status := getCluster(g, namespace, "env").Status
				g.Expect(status.AgentEnvVarsHash).NotTo(BeEmpty())
				// The agent cannot always update itself from its own bundle,
				// so a changed configuration asks for a redeployment.
				g.Expect(status.AgentConfigChanged).To(BeTrue())
				hash = status.AgentEnvVarsHash
			}).Should(Succeed())

			// A different value is a different configuration.
			updateCluster(namespace, "env", func(cluster *fleet.Cluster) {
				cluster.Spec.AgentEnvVars = []corev1.EnvVar{{Name: "HTTPS_PROXY", Value: "http://other-proxy:3128"}}
			})

			Eventually(func(g Gomega) {
				g.Expect(agentContainer(agentBundle(namespace, "env")).Env).To(ContainElement(
					corev1.EnvVar{Name: "HTTPS_PROXY", Value: "http://other-proxy:3128"},
				))
				g.Expect(getCluster(g, namespace, "env").Status.AgentEnvVarsHash).NotTo(SatisfyAny(BeEmpty(), Equal(hash)))
			}).Should(Succeed())

			// Removing the variables again both clears the hash and takes them
			// out of the agent.
			updateCluster(namespace, "env", func(cluster *fleet.Cluster) {
				cluster.Spec.AgentEnvVars = nil
			})

			Eventually(func(g Gomega) {
				g.Expect(getCluster(g, namespace, "env").Status.AgentEnvVarsHash).To(BeEmpty())
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(agentContainer(agentBundle(namespace, "env")).Env).NotTo(ContainElement(
					HaveField("Name", "HTTPS_PROXY"),
				))
			}).Should(Succeed())
		})

		It("overrides the agent's affinity and records its hash", func() {
			Expect(k8sClient.Create(ctx, newCluster(namespace, "affinity"))).To(Succeed())
			agentBundleExists(namespace, "affinity").Should(Succeed())

			// By default the agent merely prefers a node labelled for it.
			defaultAffinity := agentDeployment(agentBundle(namespace, "affinity")).Spec.Template.Spec.Affinity
			Expect(defaultAffinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution).To(HaveLen(1))
			Expect(defaultAffinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution).To(BeNil())

			// A cluster with its default agent settings has nothing to
			// redeploy. The version condition shows its status has been
			// reconciled.
			Eventually(func(g Gomega) {
				status := getCluster(g, namespace, "affinity").Status
				g.Expect(status.Conditions).To(ContainElement(HaveField("Type", fleet.ClusterConditionAgentVersionUpToDate)))
				g.Expect(status.AgentConfigChanged).To(BeFalse())
			}).Should(Succeed())

			required := &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{
								MatchExpressions: []corev1.NodeSelectorRequirement{
									{
										Key:      "kubernetes.io/arch",
										Operator: corev1.NodeSelectorOpIn,
										Values:   []string{"amd64"},
									},
								},
							},
						},
					},
				},
			}
			updateCluster(namespace, "affinity", func(cluster *fleet.Cluster) {
				cluster.Spec.AgentAffinity = required
			})

			Eventually(func(g Gomega) {
				affinity := agentDeployment(agentBundle(namespace, "affinity")).Spec.Template.Spec.Affinity
				g.Expect(affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution).To(Equal(
					required.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
				))
				g.Expect(affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution).To(BeEmpty())
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				status := getCluster(g, namespace, "affinity").Status
				g.Expect(status.AgentAffinityHash).NotTo(BeEmpty())
				// Like any other agent setting, a changed affinity asks for a
				// redeployment.
				g.Expect(status.AgentConfigChanged).To(BeTrue())
			}).Should(Succeed())

			// Dropping the override restores the default affinity and clears
			// the hash.
			updateCluster(namespace, "affinity", func(cluster *fleet.Cluster) {
				cluster.Spec.AgentAffinity = nil
			})

			Eventually(func(g Gomega) {
				g.Expect(getCluster(g, namespace, "affinity").Status.AgentAffinityHash).To(BeEmpty())
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				affinity := agentDeployment(agentBundle(namespace, "affinity")).Spec.Template.Spec.Affinity
				g.Expect(affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution).To(HaveLen(1))
			}).Should(Succeed())
		})

		It("sets the agent's resource requirements and records their hash", func() {
			Expect(k8sClient.Create(ctx, newCluster(namespace, "resources"))).To(Succeed())
			agentBundleExists(namespace, "resources").Should(Succeed())
			Expect(agentContainer(agentBundle(namespace, "resources")).Resources).To(Equal(corev1.ResourceRequirements{}))

			requirements := corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Mi")},
				Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("128Mi")},
			}
			updateCluster(namespace, "resources", func(cluster *fleet.Cluster) {
				cluster.Spec.AgentResources = requirements.DeepCopy()
			})

			Eventually(func(g Gomega) {
				resources := agentContainer(agentBundle(namespace, "resources")).Resources
				g.Expect(resources.Limits.Memory().String()).To(Equal("128Mi"))
				g.Expect(resources.Requests.Memory().String()).To(Equal("64Mi"))
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(getCluster(g, namespace, "resources").Status.AgentResourcesHash).NotTo(BeEmpty())
			}).Should(Succeed())

			// Dropping the requirements leaves the agent unconstrained again.
			updateCluster(namespace, "resources", func(cluster *fleet.Cluster) {
				cluster.Spec.AgentResources = nil
			})

			Eventually(func(g Gomega) {
				g.Expect(getCluster(g, namespace, "resources").Status.AgentResourcesHash).To(BeEmpty())
				g.Expect(agentContainer(agentBundle(namespace, "resources")).Resources).To(Equal(corev1.ResourceRequirements{}))
			}).Should(Succeed())
		})

		It("adds the cluster's tolerations to the agent in a stable order", func() {
			Expect(k8sClient.Create(ctx, newCluster(namespace, "tolerations"))).To(Succeed())
			agentBundleExists(namespace, "tolerations").Should(Succeed())

			defaultTolerations := agentDeployment(agentBundle(namespace, "tolerations")).Spec.Template.Spec.Tolerations
			Expect(defaultTolerations).To(HaveLen(2))

			// Tolerations are ordered by key, value, operator, effect and
			// finally toleration seconds, with an unset toleration second
			// sorting first. Whichever order they are written in, the agent
			// has to come out the same, or an unordered cluster spec would
			// make the agent churn. The repeated first entry checks that
			// duplicates are kept rather than collapsed.
			sorted := []corev1.Toleration{
				{Key: "a", Value: "1", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoExecute},
				{Key: "a", Value: "1", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoExecute},
				{Key: "a", Value: "1", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoExecute, TolerationSeconds: &[]int64{10}[0]},
				{Key: "a", Value: "1", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoExecute, TolerationSeconds: &[]int64{30}[0]},
				{Key: "a", Value: "1", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "a", Value: "1", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
				{Key: "a", Value: "2", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
				{Key: "b", Value: "1", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule},
			}
			expected := append(append([]corev1.Toleration{}, defaultTolerations...), sorted...)

			reversed := make([]corev1.Toleration, 0, len(sorted))
			for i := len(sorted) - 1; i >= 0; i-- {
				reversed = append(reversed, sorted[i])
			}
			updateCluster(namespace, "tolerations", func(cluster *fleet.Cluster) {
				cluster.Spec.AgentTolerations = reversed
			})

			Eventually(func(g Gomega) {
				tolerations := agentDeployment(agentBundle(namespace, "tolerations")).Spec.Template.Spec.Tolerations
				g.Expect(tolerations).To(Equal(expected))
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(getCluster(g, namespace, "tolerations").Status.AgentTolerationsHash).NotTo(BeEmpty())
			}).Should(Succeed())

			// Writing the same tolerations in their sorted order describes the
			// same agent, so the deployed agent must not be rewritten.
			resourceVersion := agentBundle(namespace, "tolerations").ResourceVersion
			updateCluster(namespace, "tolerations", func(cluster *fleet.Cluster) {
				cluster.Spec.AgentTolerations = sorted
			})

			// The bundle of a cluster added afterwards proves that the
			// namespace has been reconciled with the reordered tolerations.
			Expect(k8sClient.Create(ctx, newCluster(namespace, "tolerations-witness"))).To(Succeed())
			agentBundleExists(namespace, "tolerations-witness").Should(Succeed())

			consistently(func(g Gomega) {
				bundle := agentBundle(namespace, "tolerations")
				g.Expect(bundle.ResourceVersion).To(Equal(resourceVersion))
				g.Expect(agentDeployment(bundle).Spec.Template.Spec.Tolerations).To(Equal(expected))
			}).Should(Succeed())
		})

		It("puts the agent on the host network and records it in the cluster status", func() {
			Expect(k8sClient.Create(ctx, newCluster(namespace, "hostnetwork"))).To(Succeed())
			agentBundleExists(namespace, "hostnetwork").Should(Succeed())
			defaultStrategy := agentDeployment(agentBundle(namespace, "hostnetwork")).Spec.Strategy
			Expect(agentDeployment(agentBundle(namespace, "hostnetwork")).Spec.Template.Spec.HostNetwork).To(BeFalse())

			updateCluster(namespace, "hostnetwork", func(cluster *fleet.Cluster) {
				cluster.Spec.HostNetwork = &[]bool{true}[0]
			})

			Eventually(func(g Gomega) {
				deployment := agentDeployment(agentBundle(namespace, "hostnetwork"))
				g.Expect(deployment.Spec.Template.Spec.HostNetwork).To(BeTrue())
				// Two agents cannot hold the same host port, so the old pod
				// has to go before the new one starts.
				g.Expect(deployment.Spec.Strategy.Type).To(Equal(appsv1.RecreateDeploymentStrategyType))
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(getCluster(g, namespace, "hostnetwork").Status.AgentHostNetwork).To(BeTrue())
			}).Should(Succeed())

			// Leaving the host network restores the default rollout.
			updateCluster(namespace, "hostnetwork", func(cluster *fleet.Cluster) {
				cluster.Spec.HostNetwork = nil
			})

			Eventually(func(g Gomega) {
				g.Expect(getCluster(g, namespace, "hostnetwork").Status.AgentHostNetwork).To(BeFalse())
				deployment := agentDeployment(agentBundle(namespace, "hostnetwork"))
				g.Expect(deployment.Spec.Template.Spec.HostNetwork).To(BeFalse())
				g.Expect(deployment.Spec.Strategy).To(Equal(defaultStrategy))
			}).Should(Succeed())
		})

		It("pulls the agent image from the cluster's private registry and records it in the cluster status", func() {
			Expect(k8sClient.Create(ctx, newCluster(namespace, "private-repo"))).To(Succeed())
			agentBundleExists(namespace, "private-repo").Should(Succeed())

			updateCluster(namespace, "private-repo", func(cluster *fleet.Cluster) {
				cluster.Spec.PrivateRepoURL = "registry.example.com:5000"
			})

			Eventually(func(g Gomega) {
				g.Expect(agentContainer(agentBundle(namespace, "private-repo")).Image).To(
					Equal("registry.example.com:5000/" + config.DefaultAgentImage),
				)
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(getCluster(g, namespace, "private-repo").Status.AgentPrivateRepoURL).To(Equal("registry.example.com:5000"))
			}).Should(Succeed())

			// Without the private registry the agent pulls its image as
			// configured again.
			updateCluster(namespace, "private-repo", func(cluster *fleet.Cluster) {
				cluster.Spec.PrivateRepoURL = ""
			})

			Eventually(func(g Gomega) {
				g.Expect(getCluster(g, namespace, "private-repo").Status.AgentPrivateRepoURL).To(BeEmpty())
				g.Expect(agentContainer(agentBundle(namespace, "private-repo")).Image).To(Equal(config.DefaultAgentImage))
			}).Should(Succeed())
		})

		It("gives the agent its priority class when the cluster asks for scheduling customization", func() {
			Expect(k8sClient.Create(ctx, newCluster(namespace, "scheduling"))).To(Succeed())
			agentBundleExists(namespace, "scheduling").Should(Succeed())
			Expect(agentDeployment(agentBundle(namespace, "scheduling")).Spec.Template.Spec.PriorityClassName).To(BeEmpty())

			updateCluster(namespace, "scheduling", func(cluster *fleet.Cluster) {
				cluster.Spec.AgentSchedulingCustomization = &fleet.AgentSchedulingCustomization{
					PriorityClass: &fleet.PriorityClassSpec{Value: 1000},
				}
			})

			Eventually(func(g Gomega) {
				g.Expect(agentDeployment(agentBundle(namespace, "scheduling")).Spec.Template.Spec.PriorityClassName).To(
					Equal("fleet-agent-priority-class"),
				)
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(getCluster(g, namespace, "scheduling").Status.AgentSchedulingCustomizationHash).NotTo(BeEmpty())
			}).Should(Succeed())

			// Dropping the customization takes the priority class away again.
			updateCluster(namespace, "scheduling", func(cluster *fleet.Cluster) {
				cluster.Spec.AgentSchedulingCustomization = nil
			})

			Eventually(func(g Gomega) {
				g.Expect(getCluster(g, namespace, "scheduling").Status.AgentSchedulingCustomizationHash).To(BeEmpty())
				g.Expect(agentDeployment(agentBundle(namespace, "scheduling")).Spec.Template.Spec.PriorityClassName).To(BeEmpty())
			}).Should(Succeed())
		})

		It("follows changes to the cluster's pull secrets and records their hash", func() {
			cluster := newCluster(namespace, "pull-secrets")
			cluster.Spec.AgentPullSecrets = &[]corev1.LocalObjectReference{{Name: "first-pull-secret"}}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			agentBundleExists(namespace, "pull-secrets").Should(Succeed())
			Expect(agentDeployment(agentBundle(namespace, "pull-secrets")).Spec.Template.Spec.ImagePullSecrets).To(ConsistOf(
				corev1.LocalObjectReference{Name: "first-pull-secret"},
			))

			var firstHash string
			Eventually(func(g Gomega) {
				firstHash = getCluster(g, namespace, "pull-secrets").Status.AgentPullSecretsHash
				g.Expect(firstHash).NotTo(BeEmpty())
			}).Should(Succeed())

			updateCluster(namespace, "pull-secrets", func(cluster *fleet.Cluster) {
				cluster.Spec.AgentPullSecrets = &[]corev1.LocalObjectReference{{Name: "second-pull-secret"}}
			})

			var secondHash string
			Eventually(func(g Gomega) {
				g.Expect(agentDeployment(agentBundle(namespace, "pull-secrets")).Spec.Template.Spec.ImagePullSecrets).To(ConsistOf(
					corev1.LocalObjectReference{Name: "second-pull-secret"},
				))
				secondHash = getCluster(g, namespace, "pull-secrets").Status.AgentPullSecretsHash
				g.Expect(secondHash).NotTo(SatisfyAny(BeEmpty(), Equal(firstHash)))
			}).Should(Succeed())

			// Removing the field hands the choice back to the global
			// configuration, which names no pull secrets here, and clears the
			// hash.
			updateCluster(namespace, "pull-secrets", func(cluster *fleet.Cluster) {
				cluster.Spec.AgentPullSecrets = nil
			})

			Eventually(func(g Gomega) {
				g.Expect(agentDeployment(agentBundle(namespace, "pull-secrets")).Spec.Template.Spec.ImagePullSecrets).To(BeEmpty())
				g.Expect(getCluster(g, namespace, "pull-secrets").Status.AgentPullSecretsHash).To(BeEmpty())
			}).Should(Succeed())

			// An empty list explicitly asks for no pull secrets at all, which
			// is a setting of its own and keeps a hash.
			updateCluster(namespace, "pull-secrets", func(cluster *fleet.Cluster) {
				cluster.Spec.AgentPullSecrets = &[]corev1.LocalObjectReference{}
			})

			Eventually(func(g Gomega) {
				g.Expect(getCluster(g, namespace, "pull-secrets").Status.AgentPullSecretsHash).NotTo(
					SatisfyAny(BeEmpty(), Equal(firstHash), Equal(secondHash)),
				)
			}).Should(Succeed())
			Expect(agentDeployment(agentBundle(namespace, "pull-secrets")).Spec.Template.Spec.ImagePullSecrets).To(BeEmpty())
		})

		It("uses the cluster's own pull secrets in place of the global ones, without copying them", func() {
			setConfig(func(cfg *config.Config) {
				cfg.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "global-pull-secret"}}
			})

			// The agent namespace exists, and the cluster's pull secret exists
			// in the controller namespace a copy would be read from, so the
			// absence checks below would catch a copy being made.
			Expect(k8sClient.Create(ctx, newNamespace("cluster-agent-namespace"))).To(Succeed())
			source := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: systemNamespace, Name: "rancher-managed-pull-secret"},
				Type:       corev1.SecretTypeDockerConfigJson,
				Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
			}
			Expect(k8sClient.Create(ctx, source)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, source)).To(Succeed())
			})

			cluster := newCluster(namespace, "cluster-pull-secrets")
			cluster.Spec.AgentNamespace = "cluster-agent-namespace"
			cluster.Spec.AgentPullSecrets = &[]corev1.LocalObjectReference{{Name: "rancher-managed-pull-secret"}}
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			agentBundleExists(namespace, "cluster-pull-secrets").Should(Succeed())

			bundle := agentBundle(namespace, "cluster-pull-secrets")
			Expect(agentDeployment(bundle).Spec.Template.Spec.ImagePullSecrets).To(ConsistOf(
				corev1.LocalObjectReference{Name: "rancher-managed-pull-secret"},
			))

			// Pull secrets named on the cluster belong to whoever manages the
			// cluster, so Fleet must not copy them into the agent's namespace
			// itself.
			consistently(objectAbsent(&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: "cluster-agent-namespace", Name: "rancher-managed-pull-secret"},
			})).Should(Succeed())
		})
	})

	Describe("the agent version condition on the cluster", func() {
		It("stays unknown while the agent has never checked in", func() {
			Expect(k8sClient.Create(ctx, newCluster(namespace, "silent"))).To(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(getCluster(g, namespace, "silent").Status.Conditions).To(ContainElement(SatisfyAll(
					HaveField("Type", fleet.ClusterConditionAgentVersionUpToDate),
					HaveField("Status", corev1.ConditionUnknown),
					HaveField("Message", ContainSubstring("has not reported a version")),
				)))
			}).Should(Succeed())
		})

		It("reports an agent that checks in without a version as outdated", func() {
			Expect(k8sClient.Create(ctx, newCluster(namespace, "old-agent"))).To(Succeed())
			agentBundleExists(namespace, "old-agent").Should(Succeed())

			Eventually(func(g Gomega) {
				cluster := getCluster(g, namespace, "old-agent")
				cluster.Status.Agent.LastSeen = metav1.Now()
				g.Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(getCluster(g, namespace, "old-agent").Status.Conditions).To(ContainElement(SatisfyAll(
					HaveField("Type", fleet.ClusterConditionAgentVersionUpToDate),
					HaveField("Status", corev1.ConditionFalse),
					HaveField("Message", ContainSubstring("predates version reporting")),
					HaveField("Reason", BeEmpty()),
				)))
			}).Should(Succeed())
		})

		It("stays unknown for versions it cannot compare", func() {
			Expect(k8sClient.Create(ctx, newCluster(namespace, "unstamped"))).To(Succeed())
			agentBundleExists(namespace, "unstamped").Should(Succeed())

			Eventually(func(g Gomega) {
				cluster := getCluster(g, namespace, "unstamped")
				cluster.Status.Agent.LastSeen = metav1.Now()
				cluster.Status.Agent.Version = "not-a-version"
				g.Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(getCluster(g, namespace, "unstamped").Status.Conditions).To(ContainElement(SatisfyAll(
					HaveField("Type", fleet.ClusterConditionAgentVersionUpToDate),
					HaveField("Status", corev1.ConditionUnknown),
					HaveField("Message", ContainSubstring("cannot compare agent version")),
				)))
			}).Should(Succeed())
		})
	})

	Describe("agent settings taken from the global configuration", func() {
		It("builds the agent from the configured image, registries, check-in interval and workers", func() {
			setConfig(func(cfg *config.Config) {
				cfg.AgentImage = "system-registry.example.com/rancher/fleet-agent:v0.0.1"
				cfg.SystemDefaultRegistry = "system-registry.example.com"
				cfg.AgentImagePullPolicy = string(corev1.PullAlways)
				cfg.AgentCheckinInterval = metav1.Duration{Duration: 5 * time.Minute}
				cfg.AgentWorkers = config.AgentWorkers{BundleDeployment: "7", Drift: "3"}
			})

			Expect(k8sClient.Create(ctx, newCluster(namespace, "configured"))).To(Succeed())
			// A cluster's private registry replaces the system default
			// registry the configured image already carries, rather than
			// being stacked in front of it.
			private := newCluster(namespace, "configured-private")
			private.Spec.PrivateRepoURL = "private.example.com"
			Expect(k8sClient.Create(ctx, private)).To(Succeed())

			agentBundleExists(namespace, "configured").Should(Succeed())
			agentBundleExists(namespace, "configured-private").Should(Succeed())

			container := agentContainer(agentBundle(namespace, "configured"))
			Expect(container.Image).To(Equal("system-registry.example.com/rancher/fleet-agent:v0.0.1"))
			Expect(container.ImagePullPolicy).To(Equal(corev1.PullAlways))
			Expect(container.Env).To(ContainElements(
				corev1.EnvVar{Name: "CHECKIN_INTERVAL", Value: "5m0s"},
				corev1.EnvVar{Name: "BUNDLEDEPLOYMENT_RECONCILER_WORKERS", Value: "7"},
				corev1.EnvVar{Name: "DRIFT_RECONCILER_WORKERS", Value: "3"},
				// The agent's leader election follows the controller's
				// FLEET_AGENT_ELECTION_* environment, which the suite sets.
				corev1.EnvVar{Name: "CATTLE_ELECTION_LEASE_DURATION", Value: "30s"},
				corev1.EnvVar{Name: "CATTLE_ELECTION_RETRY_PERIOD", Value: "10s"},
				corev1.EnvVar{Name: "CATTLE_ELECTION_RENEW_DEADLINE", Value: "25s"},
			))

			Expect(agentContainer(agentBundle(namespace, "configured-private")).Image).To(
				Equal("private.example.com/rancher/fleet-agent:v0.0.1"),
			)
		})

		It("runs as many agent replicas as the controller's environment asks for", func() {
			GinkgoT().Setenv("FLEET_AGENT_REPLICA_COUNT", "3")

			Expect(k8sClient.Create(ctx, newCluster(namespace, "replicated"))).To(Succeed())
			agentBundleExists(namespace, "replicated").Should(Succeed())

			Expect(agentDeployment(agentBundle(namespace, "replicated")).Spec.Replicas).To(HaveValue(BeEquivalentTo(3)))
		})

		It("falls back to the default agent image when none is configured", func() {
			setConfig(func(cfg *config.Config) {
				cfg.AgentImage = ""
			})

			Expect(k8sClient.Create(ctx, newCluster(namespace, "default-image"))).To(Succeed())
			agentBundleExists(namespace, "default-image").Should(Succeed())

			Expect(agentContainer(agentBundle(namespace, "default-image")).Image).To(Equal(config.DefaultAgentImage))
		})

		It("passes the controller's GVK error mapping setting on to the agent, unless the cluster sets it", func() {
			GinkgoT().Setenv(config.EnvVarWranglerCheckGVKErrorMapping, "true")

			Expect(k8sClient.Create(ctx, newCluster(namespace, "inherits-mapping"))).To(Succeed())
			overriding := newCluster(namespace, "overrides-mapping")
			overriding.Spec.AgentEnvVars = []corev1.EnvVar{{Name: config.EnvVarWranglerCheckGVKErrorMapping, Value: "false"}}
			Expect(k8sClient.Create(ctx, overriding)).To(Succeed())

			agentBundleExists(namespace, "inherits-mapping").Should(Succeed())
			agentBundleExists(namespace, "overrides-mapping").Should(Succeed())

			Expect(agentContainer(agentBundle(namespace, "inherits-mapping")).Env).To(ContainElement(
				corev1.EnvVar{Name: config.EnvVarWranglerCheckGVKErrorMapping, Value: "true"},
			))

			// The cluster's own value wins and is not shadowed by a duplicate.
			var mapping []corev1.EnvVar
			for _, env := range agentContainer(agentBundle(namespace, "overrides-mapping")).Env {
				if env.Name == config.EnvVarWranglerCheckGVKErrorMapping {
					mapping = append(mapping, env)
				}
			}
			Expect(mapping).To(ConsistOf(corev1.EnvVar{Name: config.EnvVarWranglerCheckGVKErrorMapping, Value: "false"}))
		})

		It("deploys no agent bundles while agent management is turned off", func() {
			setConfig(func(cfg *config.Config) {
				cfg.ManageAgent = &[]bool{false}[0]
			})

			Expect(k8sClient.Create(ctx, newCluster(namespace, "unmanaged"))).To(Succeed())

			consistently(objectAbsent(&fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: agentBundleName("unmanaged")},
			})).Should(Succeed())

			// Turning it back on deploys the agent that was held back.
			setConfig(func(cfg *config.Config) {
				cfg.ManageAgent = nil
			})
			touchNamespace(namespace, "agent-management-enabled")

			agentBundleExists(namespace, "unmanaged").Should(Succeed())
		})

		It("withholds the agent until a configured image pull secret exists", func() {
			setConfig(func(cfg *config.Config) {
				cfg.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "configured-pull-secret"}}
			})

			Expect(k8sClient.Create(ctx, newCluster(namespace, "awaiting-secret"))).To(Succeed())

			consistently(objectAbsent(&fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: agentBundleName("awaiting-secret")},
			})).Should(Succeed())

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: systemNamespace, Name: "configured-pull-secret"},
				Type:       corev1.SecretTypeDockerConfigJson,
				Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
			})
			touchNamespace(namespace, "pull-secret-created")

			agentBundleExists(namespace, "awaiting-secret").Should(Succeed())
			Expect(agentDeployment(agentBundle(namespace, "awaiting-secret")).Spec.Template.Spec.ImagePullSecrets).To(ConsistOf(
				corev1.LocalObjectReference{Name: "configured-pull-secret"},
			))
		})

		It("deploys no agent to the local cluster while the local agent is disabled", func() {
			setConfig(func(cfg *config.Config) {
				cfg.Bootstrap.Namespace = namespace
				cfg.Bootstrap.LocalAgentDisabled = true
			})

			Expect(k8sClient.Create(ctx, newCluster(namespace, fleet.LocalClusterName))).To(Succeed())
			Expect(k8sClient.Create(ctx, newCluster(namespace, "downstream"))).To(Succeed())

			// Disabling the local agent must never reach a downstream cluster
			// that happens to share the namespace.
			agentBundleExists(namespace, "downstream").Should(Succeed())

			consistently(objectAbsent(&fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      agentBundleName(fleet.LocalClusterName),
				},
			})).Should(Succeed())
		})

		It("removes the local agent once it is disabled and restores it when re-enabled", func() {
			setConfig(func(cfg *config.Config) {
				cfg.Bootstrap.Namespace = namespace
			})

			Expect(k8sClient.Create(ctx, newCluster(namespace, fleet.LocalClusterName))).To(Succeed())
			Expect(k8sClient.Create(ctx, newCluster(namespace, "downstream"))).To(Succeed())
			agentBundleExists(namespace, fleet.LocalClusterName).Should(Succeed())
			agentBundleExists(namespace, "downstream").Should(Succeed())
			downstreamVersion := agentBundle(namespace, "downstream").ResourceVersion

			setConfig(func(cfg *config.Config) {
				cfg.Bootstrap.LocalAgentDisabled = true
			})
			touchNamespace(namespace, "local-agent-disabled")

			objectGone(&fleet.Bundle{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      agentBundleName(fleet.LocalClusterName),
				},
			}).Should(Succeed())
			Expect(agentBundle(namespace, "downstream").ResourceVersion).To(Equal(downstreamVersion))

			setConfig(func(cfg *config.Config) {
				cfg.Bootstrap.LocalAgentDisabled = false
			})
			touchNamespace(namespace, "local-agent-enabled")

			agentBundleExists(namespace, fleet.LocalClusterName).Should(Succeed())
		})
	})
})
