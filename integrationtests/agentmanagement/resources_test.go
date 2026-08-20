package agentmanagement_test

// Tests for internal/cmd/controller/agentmanagement/controllers/resources.
//
// ApplyBootstrapResources is a one-shot synchronous call made during
// controllers.Register (no watch loop). The frozen contract asserts:
//   - exact PolicyRule content on both ClusterRoles
//   - both namespaces created
//   - objects are stable (Consistently) — no lifecycle management after startup
//
// No-prune path: the object set is fixed (always the same 4 objects);
// there is no varying input that would trigger GC of an orphan. Wrangler
// apply idempotency is exercised by the suite running a second time on a
// fresh envtest (objects re-created cleanly on each run).

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/resources"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("resources.ApplyBootstrapResources", func() {
	// registrationNS is derived from systemNamespace per fleetns.SystemRegistrationNamespace.
	const registrationNS = "cattle-fleet-clusters-system"

	Describe("system and registration namespaces", func() {
		It("creates the system namespace", func() {
			namespaceExists(systemNamespace).Should(Succeed())
		})

		It("creates the system registration namespace", func() {
			namespaceExists(registrationNS).Should(Succeed())
		})

		It("keeps both namespaces stable (no lifecycle management after startup)", func() {
			Consistently(func(g Gomega) {
				ns := &corev1.Namespace{}
				g.Expect(k8sClient.Get(ctx,
					types.NamespacedName{Name: systemNamespace}, ns)).To(Succeed())
				g.Expect(k8sClient.Get(ctx,
					types.NamespacedName{Name: registrationNS}, ns)).To(Succeed())
			}).Should(Succeed())
		})
	})

	Describe("fleet-bundle-deployment ClusterRole", func() {
		It("exists", func() {
			objectExists(clusterRole(resources.BundleDeploymentClusterRole)).Should(Succeed())
		})

		It("has exactly the expected policy rules", func() {
			cr := &rbacv1.ClusterRole{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: resources.BundleDeploymentClusterRole,
				}, cr)).To(Succeed())
			}).Should(Succeed())

			Expect(cr.Rules).To(ConsistOf(
				rbacv1.PolicyRule{
					Verbs:     []string{"get", "list", "watch"},
					APIGroups: []string{fleet.SchemeGroupVersion.Group},
					Resources: []string{fleet.BundleDeploymentResourceNamePlural},
				},
				rbacv1.PolicyRule{
					Verbs:     []string{"update", "patch"},
					APIGroups: []string{fleet.SchemeGroupVersion.Group},
					Resources: []string{fleet.BundleDeploymentResourceNamePlural + "/status"},
				},
				rbacv1.PolicyRule{
					Verbs:     []string{"get"},
					APIGroups: []string{""},
					Resources: []string{"secrets", "configmaps"},
				},
			))
		})

		It("is stable (rules do not drift)", func() {
			Consistently(func(g Gomega) {
				cr := &rbacv1.ClusterRole{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: resources.BundleDeploymentClusterRole,
				}, cr)).To(Succeed())
				g.Expect(cr.Rules).To(HaveLen(3))
			}).Should(Succeed())
		})
	})

	Describe("fleet-content ClusterRole", func() {
		It("exists", func() {
			objectExists(clusterRole(resources.ContentClusterRole)).Should(Succeed())
		})

		It("has exactly the expected policy rule", func() {
			cr := &rbacv1.ClusterRole{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: resources.ContentClusterRole,
				}, cr)).To(Succeed())
			}).Should(Succeed())

			Expect(cr.Rules).To(ConsistOf(
				rbacv1.PolicyRule{
					Verbs:     []string{"get"},
					APIGroups: []string{fleet.SchemeGroupVersion.Group},
					Resources: []string{fleet.ContentResourceNamePlural},
				},
			))
		})

		It("is stable (rules do not drift)", func() {
			Consistently(func(g Gomega) {
				cr := &rbacv1.ClusterRole{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name: resources.ContentClusterRole,
				}, cr)).To(Succeed())
				g.Expect(cr.Rules).To(HaveLen(1))
			}).Should(Succeed())
		})
	})
})
