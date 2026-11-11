package bundle

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// A target that a rollout strategy holds back keeps its previous options, which
// may name a Secret the Bundle no longer names and Policy no longer allows. The
// controller refuses to refresh that target's copy of the Secret. These tests
// make sure the refusal does not keep the rollout from reaching the other
// targets, which is what eventually replaces the held back options.
var _ = Describe("Bundle DownstreamResources with a staged rollout under a Policy", Ordered, func() {
	const (
		secretName = "rollout-secret"
		policyName = "tenant-policy"
	)

	BeforeAll(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).ToNot(HaveOccurred())

		createClustersAndClusterGroups()

		policy := &v1alpha1.Policy{
			ObjectMeta:            metav1.ObjectMeta{Name: policyName, Namespace: namespace},
			RequireServiceAccount: true,
		}
		Expect(k8sClient.Create(ctx, policy)).To(Succeed())

		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, policy))).To(Succeed())
			Expect(k8sClient.Delete(ctx, ns)).ToNot(HaveOccurred())
		})
	})

	setAllowedDownstreamResources := func(allowed ...v1alpha1.AllowedDownstreamResource) {
		Eventually(func(g Gomega) {
			policy := &v1alpha1.Policy{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: policyName}, policy)).To(Succeed())
			policy.AllowedDownstreamResources = allowed
			g.Expect(k8sClient.Update(ctx, policy)).To(Succeed())
		}).Should(Succeed())
	}

	listBDs := func(g Gomega, bundleName string) []v1alpha1.BundleDeployment {
		bdList := &v1alpha1.BundleDeploymentList{}
		g.Expect(k8sClient.List(ctx, bdList, client.MatchingLabels{
			"fleet.cattle.io/bundle-name":      bundleName,
			"fleet.cattle.io/bundle-namespace": namespace,
		})).To(Succeed())
		return bdList.Items
	}

	// markDeployed stands in for the agent: it reports every BundleDeployment
	// as ready for the DeploymentID it currently has.
	markDeployed := func(g Gomega, bundleName string) {
		for _, bd := range listBDs(g, bundleName) {
			if bd.Status.Ready && bd.Status.AppliedDeploymentID == bd.Spec.DeploymentID {
				continue
			}
			bd.Status.Display.State = "Ready"
			bd.Status.AppliedDeploymentID = bd.Spec.DeploymentID
			bd.Status.Ready = true
			bd.Status.NonModified = true
			g.Expect(k8sClient.Status().Update(ctx, &bd)).To(Succeed())
		}
	}

	readyCondition := func(g Gomega, bundleName string) (corev1.ConditionStatus, string) {
		b := &v1alpha1.Bundle{}
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: bundleName}, b)).To(Succeed())
		for _, c := range b.Status.Conditions {
			if c.Type == string(v1alpha1.Ready) {
				return c.Status, c.Message
			}
		}
		return "", ""
	}

	// rolloutCompletes runs the scenario with the given clusters in the first
	// partition and the remaining cluster in the second. The second partition
	// is only rolled out once the first is done. Independently of partitions,
	// targets are processed ordered by cluster name: one, three, two.
	rolloutCompletes := func(bundleName string, first []string, rest string) {
		setAllowedDownstreamResources(v1alpha1.AllowedDownstreamResource{Kind: "Secret", Name: secretName})
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace},
			Data:       map[string][]byte{"password": []byte("first")},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		DeferCleanup(func() {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &v1alpha1.Bundle{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: bundleName},
			}))).NotTo(HaveOccurred())
			cleanupBundleDeployments(bundleName, namespace)
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, secret))).To(Succeed())
		})

		By("creating a bundle that copies the allowed secret, rolled out one cluster at a time")
		one := intstr.FromInt(1)
		bundle := &v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{Name: bundleName, Namespace: namespace},
			Spec: v1alpha1.BundleSpec{
				BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
					ServiceAccount:      "tenant-sa",
					DownstreamResources: []v1alpha1.DownstreamResource{{Kind: "Secret", Name: secretName}},
				},
				Targets: []v1alpha1.BundleTarget{{ClusterGroup: "all"}},
				RolloutStrategy: &v1alpha1.RolloutStrategy{
					Partitions: []v1alpha1.Partition{
						{
							Name:           "first",
							MaxUnavailable: &one,
							ClusterSelector: &metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{
								{Key: "cluster", Operator: metav1.LabelSelectorOpIn, Values: first},
							}},
						},
						{Name: "rest", ClusterName: rest, MaxUnavailable: &one},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, bundle)).To(Succeed())

		By("waiting until all clusters are deployed and have their copy")
		Eventually(func(g Gomega) {
			markDeployed(g, bundleName)
			bds := listBDs(g, bundleName)
			g.Expect(bds).To(HaveLen(3))
			for _, bd := range bds {
				g.Expect(bd.Spec.Options.DownstreamResources).To(HaveLen(1))
				g.Expect(bd.Status.Ready).To(BeTrue())
				g.Expect(bd.Status.AppliedDeploymentID).To(Equal(bd.Spec.DeploymentID))
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: bd.Namespace, Name: secretName}, &corev1.Secret{})).To(Succeed())
			}
			status, msg := readyCondition(g, bundleName)
			g.Expect(status).To(Equal(corev1.ConditionTrue), msg)
		}).Should(Succeed())

		By("the operator removing the secret from the Policy's allow-list")
		setAllowedDownstreamResources()
		Eventually(func(g Gomega) {
			status, msg := readyCondition(g, bundleName)
			g.Expect(status).To(Equal(corev1.ConditionFalse))
			g.Expect(msg).To(ContainSubstring("is not allowed by Policy"))
		}).Should(Succeed())

		By("the tenant removing the secret from the bundle")
		Eventually(func(g Gomega) {
			b := &v1alpha1.Bundle{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(bundle), b)).To(Succeed())
			b.Spec.DownstreamResources = nil
			g.Expect(k8sClient.Update(ctx, b)).To(Succeed())
		}).Should(Succeed())

		By("expecting the rollout to reach every cluster and the bundle to become ready")
		Eventually(func(g Gomega) {
			markDeployed(g, bundleName)
			status, msg := readyCondition(g, bundleName)
			state := []string{fmt.Sprintf("bundle ready=%s msg=%q", status, msg)}
			stale := 0
			for _, bd := range listBDs(g, bundleName) {
				promoted := bd.Spec.DeploymentID == bd.Spec.StagedDeploymentID
				if len(bd.Spec.Options.DownstreamResources) > 0 || !promoted {
					stale++
				}
				state = append(state, fmt.Sprintf("%s: downstreamResources=%d promoted=%t ready=%t",
					bd.Namespace,
					len(bd.Spec.Options.DownstreamResources), promoted, bd.Status.Ready))
			}
			desc := strings.Join(state, "\n")
			g.Expect(stale).To(BeZero(), desc)
			g.Expect(status).To(Equal(corev1.ConditionTrue), desc)
		}, 45*time.Second).Should(Succeed())
	}

	It("completes when the partitions follow the processing order", func() {
		rolloutCompletes("rollout-in-order", []string{"one", "three"}, "two")
	})

	It("completes when the last partition comes first in the processing order", func() {
		rolloutCompletes("rollout-out-of-order", []string{"three", "two"}, "one")
	})
})
