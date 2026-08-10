package agentmanagement_test

import (
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	fleetns "github.com/rancher/fleet/internal/cmd/controller/namespace"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/names"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	yaml "sigs.k8s.io/yaml"
)

var _ = Describe("ClusterRegistrationToken", func() {
	var regNamespace string

	systemRegistrationNamespace := fleetns.SystemRegistrationNamespace(systemNamespace)

	BeforeEach(func() {
		ns := newGeneratedNamespace("cluster-registration-token-test-")
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		regNamespace = ns.Name
	})

	// waitForServiceAccount waits for the ServiceAccount the handler creates
	// for token and returns its name.
	waitForServiceAccount := func(token *fleet.ClusterRegistrationToken) string {
		GinkgoHelper()

		saName := names.SafeConcatName(token.Name, string(token.UID))
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: token.Namespace, Name: saName}, &corev1.ServiceAccount{})).To(Succeed())
		}).Should(Succeed())
		return saName
	}

	// unblockServiceAccountTokenSecret waits for the "<sa>-token" Secret the
	// handler creates for saName and fills in fake token data, standing in
	// for the token controller that would populate it in a real cluster
	// (envtest runs no controller-manager to do this on its own). Without
	// this, the handler blocks indefinitely waiting for the token to appear.
	unblockServiceAccountTokenSecret := func(namespace, saName, tokenValue string) {
		GinkgoHelper()

		key := types.NamespacedName{Namespace: namespace, Name: saName + "-token"}
		// Get and update must share the retry, or a resourceVersion conflict
		// between them fails the spec instead of being retried. Only the token
		// key is set, so the rest of a service account token Secret's data
		// (ca.crt, namespace) survives if anything ever populates it.
		Eventually(func(g Gomega) {
			secret := &corev1.Secret{}
			g.Expect(k8sClient.Get(ctx, key, secret)).To(Succeed())
			if secret.Data == nil {
				secret.Data = map[string][]byte{}
			}
			secret.Data[corev1.ServiceAccountTokenKey] = []byte(tokenValue)
			g.Expect(k8sClient.Update(ctx, secret)).To(Succeed())
		}).Should(Succeed())
	}

	// createTokenWithPopulatedSecret creates a token with the given TTL,
	// unblocks the ServiceAccount token Secret the handler waits on (see
	// unblockServiceAccountTokenSecret), and drives the token's own
	// reconciliation until the resulting cluster-registration-values Secret
	// reflects that token data. Returns the created token and the
	// ServiceAccount name.
	createTokenWithPopulatedSecret := func(name string, ttl *metav1.Duration, tokenValue string) (*fleet.ClusterRegistrationToken, string) {
		GinkgoHelper()

		token := newClusterRegistrationToken(regNamespace, name, ttl)
		Expect(k8sClient.Create(ctx, token)).To(Succeed())

		saName := waitForServiceAccount(token)
		unblockServiceAccountTokenSecret(regNamespace, saName, tokenValue)

		// The handler only re-reads the SA token Secret through its cache the
		// instant it first observes a populated live Secret, and nothing
		// re-triggers it on later Secret changes. Nudge the token itself
		// (which is watched directly) until the derived values Secret
		// reflects the token data, so this does not depend on informer cache
		// timing relative to the update above.
		valuesSecretKey := types.NamespacedName{Namespace: regNamespace, Name: token.Name}
		Eventually(func(g Gomega) {
			var current fleet.ClusterRegistrationToken
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: token.Name}, &current)).To(Succeed())
			if current.Annotations == nil {
				current.Annotations = map[string]string{}
			}
			current.Annotations["test.fleet.cattle.io/nudge"] = strconv.FormatInt(time.Now().UnixNano(), 10)
			g.Expect(k8sClient.Update(ctx, &current)).To(Succeed())

			var values corev1.Secret
			g.Expect(k8sClient.Get(ctx, valuesSecretKey, &values)).To(Succeed())

			var parsed map[string]any
			g.Expect(yaml.Unmarshal(values.Data[config.ImportTokenSecretValuesKey], &parsed)).To(Succeed())
			g.Expect(parsed["token"]).To(Equal(tokenValue))
			// Keep the suite's timeout: this is the slowest wait in the file,
			// gated on a 2s durations.ServiceTokenSleep boundary plus an
			// informer cache sync. Only the interval is tightened from the
			// suite's 3s default, and not below 500ms, because every iteration
			// issues a token Update.
		}, utils.Timeout, 500*time.Millisecond).Should(Succeed())

		return token, saName
	}

	Describe("tokens without a usable TTL are rejected when enforceTTL is enabled", func() {
		It("deletes a token with no TTL and creates no resources for it", func() {
			token := newClusterRegistrationToken(regNamespace, "no-ttl", nil)
			Expect(k8sClient.Create(ctx, token)).To(Succeed())

			objectGone(token).Should(Succeed())

			saName := names.SafeConcatName(token.Name, string(token.UID))
			Consistently(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: saName}, &corev1.ServiceAccount{})
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}).Should(Succeed())
		})

		It("deletes a token whose TTL duration is zero", func() {
			token := newClusterRegistrationToken(regNamespace, "zero-ttl", &metav1.Duration{Duration: 0})
			Expect(k8sClient.Create(ctx, token)).To(Succeed())

			objectGone(token).Should(Succeed())
		})
	})

	Describe("a valid token", func() {
		It("creates a ServiceAccount, a Role scoped to creating clusterregistrations, and a RoleBinding, all in the token's own namespace", func() {
			_, saName := createTokenWithPopulatedSecret("valid-token", &metav1.Duration{Duration: 24 * time.Hour}, "fake-sa-token")

			var sa corev1.ServiceAccount
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: saName}, &sa)).To(Succeed())
			Expect(sa.Labels).To(HaveKeyWithValue(fleet.ManagedLabel, "true"))

			roleName := names.SafeConcatName(saName, "role")
			var role rbacv1.Role
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: roleName}, &role)).To(Succeed())
			Expect(role.Rules).To(ConsistOf(rbacv1.PolicyRule{
				Verbs:     []string{"create"},
				APIGroups: []string{fleet.SchemeGroupVersion.Group},
				Resources: []string{fleet.ClusterRegistrationResourceNamePlural},
			}), "the token handler must not grant access to secrets; that is the clusterregistration controller's responsibility")

			roleBindingName := names.SafeConcatName(saName, "to", "role")
			var rb rbacv1.RoleBinding
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: roleBindingName}, &rb)).To(Succeed())
			Expect(rb.RoleRef).To(Equal(rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "Role",
				Name:     roleName,
			}))
			Expect(rb.Subjects).To(ConsistOf(rbacv1.Subject{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: regNamespace,
			}))

			Expect(regNamespace).NotTo(Equal(systemRegistrationNamespace),
				"sanity check: the generated namespace must not collide with the system registration namespace")
			Expect(sa.Namespace).NotTo(Equal(systemRegistrationNamespace))
			Expect(role.Namespace).NotTo(Equal(systemRegistrationNamespace))
			Expect(rb.Namespace).NotTo(Equal(systemRegistrationNamespace))
		})

		It("populates the cluster-registration-values Secret and status with the service account token", func() {
			token, _ := createTokenWithPopulatedSecret("valid-token-values", &metav1.Duration{Duration: 24 * time.Hour}, "expected-token-value")

			var values corev1.Secret
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: token.Name}, &values)).To(Succeed())
			Expect(values.Labels).To(HaveKeyWithValue(fleet.ManagedLabel, "true"))
			Expect(values.Type).To(BeEquivalentTo("fleet.cattle.io/cluster-registration-values"))

			var parsed map[string]any
			Expect(yaml.Unmarshal(values.Data[config.ImportTokenSecretValuesKey], &parsed)).To(Succeed())
			Expect(parsed["clusterNamespace"]).To(Equal(regNamespace))
			Expect(parsed["systemRegistrationNamespace"]).To(Equal(systemRegistrationNamespace))
			Expect(parsed["tokenName"]).To(Equal(token.Name))
			Expect(parsed["token"]).To(Equal("expected-token-value"))
			Expect(parsed["apiServerURL"]).To(Equal(config.Get().APIServerURL))
			Expect(parsed["apiServerCA"]).To(Equal(string(config.Get().APIServerCA)))
			// systemNamespace equals config.DefaultNamespace in this suite, so the
			// "internal" override block is never populated; see PROGRESS.md.
			Expect(parsed).NotTo(HaveKey("internal"))

			Eventually(func(g Gomega) {
				var current fleet.ClusterRegistrationToken
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: token.Name}, &current)).To(Succeed())
				g.Expect(current.Status.SecretName).To(Equal(token.Name))
				g.Expect(current.Status.Expires).NotTo(BeNil())
				g.Expect(current.Status.Expires.Time).To(BeTemporally("~", current.CreationTimestamp.Add(24*time.Hour), 10*time.Second))
			}).Should(Succeed())
		})
	})

	Describe("expiration", func() {
		It("deletes a token once its TTL has elapsed", func() {
			// Settle the token first, then shorten its TTL to one that has
			// certainly already elapsed (expiry is measured from
			// CreationTimestamp, and setup takes seconds). Creating the token
			// with a short TTL up front instead races the controller against
			// itself: whether the reconcile that creates the ServiceAccount
			// beats the one EnqueueAfter(TTL) schedules decides the outcome,
			// and if expiry wins, apply prunes the ServiceAccount and the
			// helpers waiting on it spin until they time out.
			token, _ := createTokenWithPopulatedSecret("expired-ttl", &metav1.Duration{Duration: time.Hour}, "expired-ttl-value")

			Eventually(func(g Gomega) {
				var current fleet.ClusterRegistrationToken
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: token.Name}, &current)).To(Succeed())
				current.Spec.TTL = &metav1.Duration{Duration: time.Nanosecond}
				g.Expect(k8sClient.Update(ctx, &current)).To(Succeed())
			}).Should(Succeed())

			objectGone(token).Should(Succeed())
		})

		It("does not delete a token before its TTL has elapsed", func() {
			// Populate the SA token Secret rather than creating the token
			// bare: the reconcile that observes the ServiceAccount otherwise
			// parks forever in the handler's unbounded wait for that Secret
			// (envtest populates no service account tokens), holding a worker
			// for the rest of the suite. This also settles the token, so the
			// assertion below observes a fully reconciled object.
			token, _ := createTokenWithPopulatedSecret("long-ttl", &metav1.Duration{Duration: time.Hour}, "long-ttl-value")

			Consistently(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: token.Name}, &fleet.ClusterRegistrationToken{})).To(Succeed())
			}).Should(Succeed())
		})
	})

	Describe("deleting a token", func() {
		It("removes the ServiceAccount, Role, RoleBinding, and values Secret it created", func() {
			token, saName := createTokenWithPopulatedSecret("pruned-token", &metav1.Duration{Duration: 24 * time.Hour}, "pruned-token-value")

			sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: regNamespace, Name: saName}}
			role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: regNamespace, Name: names.SafeConcatName(saName, "role")}}
			roleBinding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Namespace: regNamespace, Name: names.SafeConcatName(saName, "to", "role")}}
			values := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: regNamespace, Name: token.Name}}

			// Confirm the full set exists before deletion, so their absence
			// afterwards is actually caused by pruning and not by them never
			// having been created.
			objectExists(sa).Should(Succeed())
			objectExists(role).Should(Succeed())
			objectExists(roleBinding).Should(Succeed())
			objectExists(values).Should(Succeed())

			Expect(k8sClient.Delete(ctx, token)).To(Succeed())
			objectGone(token).Should(Succeed())

			objectGone(sa).Should(Succeed())
			objectGone(role).Should(Succeed())
			objectGone(roleBinding).Should(Succeed())
			objectGone(values).Should(Succeed())
		})
	})
})
