package bundle

import (
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/experimental"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// On this release branch the downstream-resource copy is gated by the
// EXPERIMENTAL_COPY_RESOURCES_DOWNSTREAM flag, and so is the Policy check of
// downstreamResources.
var _ = Describe("Bundle DownstreamResources with a Policy", Ordered, func() {
	const (
		bundleName = "policy-downstream-resources"
		secretName = "operator-secret"
		policyName = "tenant-policy"
	)

	var secret *corev1.Secret

	BeforeAll(func() {
		var err error
		namespace, err = utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		Expect(k8sClient.Create(ctx, ns)).ToNot(HaveOccurred())

		createClustersAndClusterGroups()

		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, ns)).ToNot(HaveOccurred())
		})
	})

	BeforeEach(func() {
		policy := &v1alpha1.Policy{
			ObjectMeta:            metav1.ObjectMeta{Name: policyName, Namespace: namespace},
			RequireServiceAccount: true,
		}
		Expect(k8sClient.Create(ctx, policy)).To(Succeed())

		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace},
			Data:       map[string][]byte{"password": []byte("first")},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		DeferCleanup(func() {
			// Without garbage collection in envtest, staged copies outlive their
			// BundleDeployments, so delete them for the next spec.
			bdList := &v1alpha1.BundleDeploymentList{}
			Expect(k8sClient.List(ctx, bdList, client.MatchingLabels{
				"fleet.cattle.io/bundle-name":      bundleName,
				"fleet.cattle.io/bundle-namespace": namespace,
			})).To(Succeed())
			for _, bd := range bdList.Items {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Namespace: bd.Namespace, Name: secretName},
				}))).To(Succeed())
			}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &v1alpha1.Bundle{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: bundleName},
			}))).NotTo(HaveOccurred())
			// The Bundle has a finalizer, so wait until it is gone before the
			// next spec creates one with the same name.
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: bundleName}, &v1alpha1.Bundle{})
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "bundle still exists: %v", err)
			}).Should(Succeed())
			cleanupBundleDeployments(bundleName, namespace)
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, secret))).To(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &v1alpha1.Policy{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: policyName},
			}))).To(Succeed())
		})
	})

	enableCopy := func() {
		Expect(os.Setenv(experimental.CopyResourcesDownstreamFlag, "true")).To(Succeed())
		DeferCleanup(func() {
			Expect(os.Unsetenv(experimental.CopyResourcesDownstreamFlag)).To(Succeed())
		})
	}

	setAllowedDownstreamResources := func(allowed ...v1alpha1.AllowedDownstreamResource) {
		Eventually(func(g Gomega) {
			policy := &v1alpha1.Policy{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: policyName}, policy)).To(Succeed())
			policy.AllowedDownstreamResources = allowed
			g.Expect(k8sClient.Update(ctx, policy)).To(Succeed())
		}).Should(Succeed())
	}

	readyCondition := func(g Gomega) *genericcondition.GenericCondition {
		b := &v1alpha1.Bundle{}
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: bundleName}, b)).To(Succeed())
		for i := range b.Status.Conditions {
			if b.Status.Conditions[i].Type == string(v1alpha1.Ready) {
				return &b.Status.Conditions[i]
			}
		}
		return nil
	}

	expectRefused := func() {
		Eventually(func(g Gomega) {
			ready := readyCondition(g)
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(corev1.ConditionFalse))
			g.Expect(ready.Message).To(ContainSubstring("downstream resource Secret " + secretName + " is not allowed by Policy"))
		}).Should(Succeed())
	}

	listBDs := func(g Gomega) []v1alpha1.BundleDeployment {
		bdList := &v1alpha1.BundleDeploymentList{}
		g.Expect(k8sClient.List(ctx, bdList, client.MatchingLabels{
			"fleet.cattle.io/bundle-name":      bundleName,
			"fleet.cattle.io/bundle-namespace": namespace,
		})).To(Succeed())
		return bdList.Items
	}

	stagedCopies := func(g Gomega) []corev1.Secret {
		bds := listBDs(g)
		g.Expect(bds).To(HaveLen(3))
		copies := []corev1.Secret{}
		for _, bd := range bds {
			copied := corev1.Secret{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: bd.Namespace, Name: secretName}, &copied)).To(Succeed())
			copies = append(copies, copied)
		}
		return copies
	}

	createBundle := func() {
		bundle := &v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{Name: bundleName, Namespace: namespace},
			Spec: v1alpha1.BundleSpec{
				BundleDeploymentOptions: v1alpha1.BundleDeploymentOptions{
					ServiceAccount:      "tenant-sa",
					DownstreamResources: []v1alpha1.DownstreamResource{{Kind: "Secret", Name: secretName}},
				},
				Targets: []v1alpha1.BundleTarget{{ClusterGroup: "all"}},
			},
		}
		Expect(k8sClient.Create(ctx, bundle)).To(Succeed())
	}

	It("copies only Secrets the Policy allows, and freezes copies when the Policy stops allowing them", func() {
		enableCopy()

		By("creating a bundle referencing a secret the Policy does not allow")
		createBundle()

		By("verifying the bundle is refused and nothing is deployed or copied")
		expectRefused()
		Consistently(func(g Gomega) {
			g.Expect(listBDs(g)).To(BeEmpty())
		}, 3*time.Second).Should(Succeed())

		By("verifying an entry for another name or kind does not allow the copy")
		setAllowedDownstreamResources(
			v1alpha1.AllowedDownstreamResource{Kind: "Secret", Name: "other"},
			v1alpha1.AllowedDownstreamResource{Kind: "ConfigMap", Name: secretName},
		)
		expectRefused()
		Consistently(func(g Gomega) {
			g.Expect(listBDs(g)).To(BeEmpty())
		}, 3*time.Second).Should(Succeed())

		By("allowing the secret by pattern in the Policy, which re-triggers the refused bundle")
		setAllowedDownstreamResources(v1alpha1.AllowedDownstreamResource{Kind: "Secret", NamePattern: "operator-.*"})
		Eventually(func(g Gomega) {
			for _, c := range stagedCopies(g) {
				g.Expect(c.Data).To(HaveKeyWithValue("password", []byte("first")))
			}
		}).Should(Succeed())

		By("removing the entry from the Policy and changing the data")
		setAllowedDownstreamResources()
		Eventually(func(g Gomega) {
			src := &corev1.Secret{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(secret), src)).To(Succeed())
			src.Data["password"] = []byte("second")
			g.Expect(k8sClient.Update(ctx, src)).To(Succeed())
		}).Should(Succeed())

		By("verifying the bundle is refused and existing copies are kept but not updated")
		expectRefused()
		Consistently(func(g Gomega) {
			for _, c := range stagedCopies(g) {
				g.Expect(c.Data).To(HaveKeyWithValue("password", []byte("first")))
			}
		}, 3*time.Second).Should(Succeed())
	})

	It("checks the bundle again when a Policy is deleted or created", func() {
		enableCopy()

		By("creating a bundle referencing a secret the Policy does not allow")
		createBundle()
		expectRefused()

		By("deleting the Policy, which re-triggers the refused bundle")
		Expect(k8sClient.Delete(ctx, &v1alpha1.Policy{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: policyName},
		})).To(Succeed())
		Eventually(func(g Gomega) {
			for _, c := range stagedCopies(g) {
				g.Expect(c.Data).To(HaveKeyWithValue("password", []byte("first")))
			}
			ready := readyCondition(g)
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Message).NotTo(ContainSubstring("is not allowed by Policy"))
		}).Should(Succeed())

		By("creating a Policy again, which refuses the deployed bundle")
		Expect(k8sClient.Create(ctx, &v1alpha1.Policy{
			ObjectMeta:            metav1.ObjectMeta{Name: policyName, Namespace: namespace},
			RequireServiceAccount: true,
		})).To(Succeed())
		expectRefused()
	})

	It("does not refuse the bundle when copying is disabled", func() {
		By("creating a bundle referencing a secret the Policy does not allow")
		createBundle()

		By("verifying the bundle is deployed and nothing is copied")
		Eventually(func(g Gomega) {
			g.Expect(listBDs(g)).To(HaveLen(3))
		}).Should(Succeed())
		Consistently(func(g Gomega) {
			ready := readyCondition(g)
			if ready != nil {
				g.Expect(ready.Message).NotTo(ContainSubstring("is not allowed by Policy"))
			}
			for _, bd := range listBDs(g) {
				copied := corev1.Secret{}
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: bd.Namespace, Name: secretName}, &copied)
				g.Expect(client.IgnoreNotFound(err)).To(Succeed())
				g.Expect(err).To(HaveOccurred())
			}
		}, 3*time.Second).Should(Succeed())
	})
})
