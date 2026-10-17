package agentmanagement_test

import (
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/resources"
	fleetns "github.com/rancher/fleet/internal/cmd/controller/namespace"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/names"
	"github.com/rancher/fleet/internal/registration"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// agentCredentialSecretType is the type of the registration Secret the agent
// reads its upstream credentials from.
const agentCredentialSecretType = "fleet.cattle.io/agent-credential"

// negativeWindow is how long a spec watches for something that must not
// happen. Gomega's 100ms Consistently default is shorter than a single
// reconcile round-trip, so a negative assertion given that default passes long
// before the controller could have reacted at all.
const negativeWindow = 5 * time.Second

var systemRegistrationNamespace = fleetns.SystemRegistrationNamespace(systemNamespace)

// consistently watches body over negativeWindow, for assertions that something
// does not appear, does not come back, or does not change.
func consistently(body func(Gomega)) AsyncAssertion {
	return Consistently(body).WithTimeout(negativeWindow).WithPolling(250 * time.Millisecond)
}

// waitPastCreationSecond blocks until the wall clock has left the second ts
// falls in. CreationTimestamp has one-second resolution and shouldDelete
// compares two of them strictly, so a registration created within the same
// second as an older one would silently fail to supersede it. In practice the
// wait is already over before it is called, but the spec must not rest on that.
func waitPastCreationSecond(ts metav1.Time) {
	if d := time.Until(ts.Add(time.Second)); d > 0 {
		time.Sleep(d)
	}
}

// registeredCluster waits for the Cluster carrying clientID in namespace to
// exist and to have been assigned a cluster namespace, then returns it.
func registeredCluster(namespace, clientID string) *fleet.Cluster {
	GinkgoHelper()

	found := &fleet.Cluster{}
	Eventually(func(g Gomega) {
		list := &fleet.ClusterList{}
		g.Expect(k8sClient.List(ctx, list, client.InNamespace(namespace))).To(Succeed())

		matches := []fleet.Cluster{}
		for _, c := range list.Items {
			if c.Spec.ClientID == clientID {
				matches = append(matches, c)
			}
		}
		g.Expect(matches).To(HaveLen(1), "exactly one cluster must carry the client ID")
		g.Expect(matches[0].Status.Namespace).NotTo(BeEmpty())
		*found = matches[0]
	}).Should(Succeed())

	return found
}

// grantRegistration drives cr through to the granted state and returns the
// Cluster it registered against together with the name of the request
// ServiceAccount created for it.
//
// Granting blocks in the controller until the request ServiceAccount's token
// Secret carries a token, so the spec supplies saToken itself; see
// populateServiceAccountTokenSecret.
func grantRegistration(cr *fleet.ClusterRegistration, saToken string) (*fleet.Cluster, string) {
	GinkgoHelper()

	cluster := registeredCluster(cr.Namespace, cr.Spec.ClientID)

	saName := names.SafeConcatName(cr.Name, string(cr.UID))
	objectExists(&corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Namespace: cluster.Status.Namespace, Name: saName},
	}).Should(Succeed())
	populateServiceAccountTokenSecret(cluster.Status.Namespace, saName, saToken)

	Eventually(func(g Gomega) {
		current := &fleet.ClusterRegistration{}
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), current)).To(Succeed())
		g.Expect(current.Status.Granted).To(BeTrue())
		g.Expect(current.Status.ClusterName).To(Equal(cluster.Name))
	}).Should(Succeed())

	return cluster, saName
}

// createSettledToken creates a ClusterRegistrationToken that outlives the spec
// and supplies the token for the ServiceAccount its own controller creates, so
// that controller does not stay parked waiting for one. The returned token
// carries the UID the API server assigned, which Create writes back into the
// object in place; the service account name is derived from it.
func createSettledToken(namespace, name string) *fleet.ClusterRegistrationToken {
	GinkgoHelper()

	token := newClusterRegistrationToken(namespace, name, &metav1.Duration{Duration: time.Hour})
	Expect(k8sClient.Create(ctx, token)).To(Succeed())

	saName := names.SafeConcatName(token.Name, string(token.UID))
	objectExists(&corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: saName},
	}).Should(Succeed())
	populateServiceAccountTokenSecret(namespace, saName, "registration-token-"+name)

	return token
}

// credentialSecretName is the name of the registration Secret granted to cr.
func credentialSecretName(cr *fleet.ClusterRegistration) string {
	return registration.SecretName(cr.Spec.ClientID, cr.Spec.ClientRandom)
}

// registrationObjects lists the objects a granted ClusterRegistration owns, so
// specs can assert they are created and, later, pruned.
func registrationObjects(cr *fleet.ClusterRegistration, cluster *fleet.Cluster, saName string) []client.Object {
	return []client.Object{
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Namespace: systemRegistrationNamespace, Name: credentialSecretName(cr)}},
		&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Status.Namespace, Name: saName}},
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{
			Namespace: cr.Namespace, Name: cr.Name}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
			Namespace: cr.Namespace, Name: cr.Name}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Status.Namespace, Name: cr.Name}},
		&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{
			Name: names.SafeConcatName(cr.Name, "content")}},
	}
}

// credentialGrantObjects lists the Role and RoleBinding that scope a
// registration token's service account to the single credential Secret granted
// to cr, for the agent-initiated flow where the role is named after cr.
func credentialGrantObjects(cr *fleet.ClusterRegistration) []client.Object {
	credRoleName := names.SafeConcatName(cr.Name, "creds")
	return []client.Object{
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{
			Namespace: systemRegistrationNamespace, Name: credRoleName}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
			Namespace: systemRegistrationNamespace, Name: credRoleName}},
	}
}

var _ = Describe("ClusterRegistration", func() {
	var (
		regNamespace string
		token        *fleet.ClusterRegistrationToken
	)

	BeforeEach(func() {
		ns := newGeneratedNamespace("cluster-registration-test-")
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		regNamespace = ns.Name
		token = createSettledToken(regNamespace, "registration-token")
	})

	Describe("agent-initiated registration", func() {
		It("creates a cluster for the client ID and grants the registration", func() {
			cr := newClusterRegistration(regNamespace, "agent-request", "agent-client-id", "random-a", token.Name)
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			cluster, _ := grantRegistration(cr, "agent-sa-token")

			Expect(cluster.Name).To(Equal(names.SafeConcatName("cluster", names.KeyHash("agent-client-id"))),
				"the cluster name is derived from the client ID, so a re-registering agent finds its own cluster")
			Expect(cluster.Namespace).To(Equal(regNamespace))
			Expect(cluster.Labels).To(HaveKeyWithValue(fleet.ClusterAnnotation, cluster.Name))

			By("making the cluster the owner of the registration")
			granted := &fleet.ClusterRegistration{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), granted)).To(Succeed())
			Expect(granted.OwnerReferences).To(ConsistOf(metav1.OwnerReference{
				APIVersion: fleet.SchemeGroupVersion.String(),
				Kind:       "Cluster",
				Name:       cluster.Name,
				UID:        cluster.UID,
			}))
		})

		It("writes the agent's upstream credentials to a registration Secret in the system registration namespace", func() {
			cr := newClusterRegistration(regNamespace, "credential-request", "credential-client-id", "random-b", token.Name)
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			cluster, _ := grantRegistration(cr, "credential-sa-token")

			credentials := &corev1.Secret{}
			key := types.NamespacedName{Namespace: systemRegistrationNamespace, Name: credentialSecretName(cr)}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, key, credentials)).To(Succeed())
			}).Should(Succeed())

			Expect(credentials.Type).To(BeEquivalentTo(agentCredentialSecretType))
			Expect(credentials.Labels).To(HaveKeyWithValue(fleet.ClusterAnnotation, cluster.Name))
			Expect(credentials.Labels).To(HaveKeyWithValue(fleet.ManagedLabel, "true"))
			Expect(credentials.Data).To(Equal(map[string][]byte{
				"token":               []byte("credential-sa-token"),
				"deploymentNamespace": []byte(cluster.Status.Namespace),
				"clusterNamespace":    []byte(regNamespace),
				"clusterName":         []byte(cluster.Name),
				"systemNamespace":     []byte(systemNamespace),
			}))
		})

		It("grants the request service account access to the cluster status, bundle deployments and contents", func() {
			cr := newClusterRegistration(regNamespace, "rbac-request", "rbac-client-id", "random-c", token.Name)
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			cluster, saName := grantRegistration(cr, "rbac-sa-token")
			subject := rbacv1.Subject{Kind: "ServiceAccount", Name: saName, Namespace: cluster.Status.Namespace}

			By("annotating the request service account with the registration it belongs to")
			requestSA := &corev1.ServiceAccount{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: cluster.Status.Namespace, Name: saName}, requestSA)).To(Succeed())
			Expect(requestSA.Labels).To(HaveKeyWithValue(fleet.ManagedLabel, "true"))
			Expect(requestSA.Annotations).To(HaveKeyWithValue(fleet.ClusterAnnotation, cluster.Name))
			// The controller watches ServiceAccounts and maps them back to a
			// registration through these two annotations, so a registration
			// waiting on its service account is re-enqueued when it appears.
			Expect(requestSA.Annotations).To(HaveKeyWithValue(fleet.ClusterRegistrationAnnotation, cr.Name))
			Expect(requestSA.Annotations).To(HaveKeyWithValue(fleet.ClusterRegistrationNamespaceAnnotation, cr.Namespace))

			By("patching only its own cluster's status, from the registration namespace")
			statusRole := &rbacv1.Role{}
			objectExists(&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Namespace: regNamespace, Name: cr.Name}}).Should(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: cr.Name}, statusRole)).To(Succeed())
			Expect(statusRole.Labels).To(HaveKeyWithValue(fleet.ManagedLabel, "true"))
			Expect(statusRole.Rules).To(ConsistOf(rbacv1.PolicyRule{
				Verbs:         []string{"patch"},
				APIGroups:     []string{fleet.SchemeGroupVersion.Group},
				Resources:     []string{fleet.ClusterResourceNamePlural + "/status"},
				ResourceNames: []string{cluster.Name},
			}))

			statusBinding := &rbacv1.RoleBinding{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: regNamespace, Name: cr.Name}, statusBinding)).To(Succeed())
			Expect(statusBinding.RoleRef).To(Equal(rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName, Kind: "Role", Name: cr.Name,
			}))
			Expect(statusBinding.Subjects).To(ConsistOf(subject))

			By("managing bundle deployments inside its own cluster namespace")
			deploymentBinding := &rbacv1.RoleBinding{}
			objectExists(&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
				Namespace: cluster.Status.Namespace, Name: cr.Name}}).Should(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: cluster.Status.Namespace, Name: cr.Name}, deploymentBinding)).To(Succeed())
			Expect(deploymentBinding.RoleRef).To(Equal(rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: resources.BundleDeploymentClusterRole,
			}))
			Expect(deploymentBinding.Subjects).To(ConsistOf(subject))

			By("reading contents cluster-wide, since content names are random")
			contentBinding := &rbacv1.ClusterRoleBinding{}
			contentName := names.SafeConcatName(cr.Name, "content")
			objectExists(&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: contentName}}).Should(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: contentName}, contentBinding)).To(Succeed())
			Expect(contentBinding.RoleRef).To(Equal(rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: resources.ContentClusterRole,
			}))
			Expect(contentBinding.Subjects).To(ConsistOf(subject))
		})

		It("lets only the registration token's service account read the new registration Secret", func() {
			token := createSettledToken(regNamespace, "agent-registration-token")

			cr := newClusterRegistration(regNamespace, "token-labelled-request", "token-labelled-client-id", "random-d", token.Name)
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			grantRegistration(cr, "token-labelled-sa-token")

			credRoleName := names.SafeConcatName(cr.Name, "creds")
			role := &rbacv1.Role{}
			objectExists(&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{
				Namespace: systemRegistrationNamespace, Name: credRoleName}}).Should(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: systemRegistrationNamespace, Name: credRoleName}, role)).To(Succeed())
			Expect(role.Labels).To(HaveKeyWithValue(fleet.ManagedLabel, "true"))
			Expect(role.Rules).To(ConsistOf(rbacv1.PolicyRule{
				Verbs:         []string{"get"},
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: []string{credentialSecretName(cr)},
			}), "the grant must name the single credential Secret, never all secrets in the namespace")

			binding := &rbacv1.RoleBinding{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: systemRegistrationNamespace, Name: credRoleName}, binding)).To(Succeed())
			Expect(binding.RoleRef).To(Equal(rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName, Kind: "Role", Name: credRoleName,
			}))
			Expect(binding.Subjects).To(ConsistOf(rbacv1.Subject{
				Kind:      "ServiceAccount",
				Name:      names.SafeConcatName(token.Name, string(token.UID)),
				Namespace: regNamespace,
			}))
		})

		It("keeps the granted registration when a new request names no token", func() {
			first := newClusterRegistration(regNamespace, "first-request", "unlabelled-client-id", "random-first", token.Name)
			Expect(k8sClient.Create(ctx, first)).To(Succeed())
			cluster, saName := grantRegistration(first, "first-sa-token")

			cr := newClusterRegistration(regNamespace, "unlabelled-request", "unlabelled-client-id", "random-e", "")
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			consistently(func(g Gomega) {
				current := &fleet.ClusterRegistration{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), current)).To(Succeed())
				g.Expect(current.Status.Granted).To(BeFalse())
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(first), &fleet.ClusterRegistration{})).To(Succeed())
				for _, obj := range credentialGrantObjects(cr) {
					objectAbsent(obj)(g)
				}
				for _, obj := range registrationObjects(first, cluster, saName) {
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj)).To(Succeed())
				}
			}).Should(Succeed())
		})

		It("keeps pending resources and resumes when the missing token appears", func() {
			first := newClusterRegistration(regNamespace, "first-request", "stale-token-client-id", "random-first", token.Name)
			Expect(k8sClient.Create(ctx, first)).To(Succeed())
			cluster, firstSAName := grantRegistration(first, "first-sa-token")
			Expect(k8sClient.Delete(ctx, token)).To(Succeed())
			objectGone(token).Should(Succeed())

			waitPastCreationSecond(first.CreationTimestamp)
			cr := newClusterRegistration(regNamespace, "stale-token-request", "stale-token-client-id", "random-f", token.Name)
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			saName := names.SafeConcatName(cr.Name, string(cr.UID))
			sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Namespace: cluster.Status.Namespace, Name: saName}}
			objectExists(sa).Should(Succeed())
			populateServiceAccountTokenSecret(cluster.Status.Namespace, saName, "stale-token-sa-token")
			Eventually(func(g Gomega) {
				current := &fleet.ClusterRegistration{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), current)).To(Succeed())
				g.Expect(current.Status.ClusterName).To(Equal(cluster.Name))
			}).Should(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sa), sa)).To(Succeed())
			saVersion := sa.ResourceVersion

			consistently(func(g Gomega) {
				current := &fleet.ClusterRegistration{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), current)).To(Succeed())
				g.Expect(current.Status.Granted).To(BeFalse())
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(sa), sa)).To(Succeed())
				g.Expect(sa.ResourceVersion).To(Equal(saVersion))
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(first), &fleet.ClusterRegistration{})).To(Succeed())
				for _, obj := range credentialGrantObjects(cr) {
					objectAbsent(obj)(g)
				}
				for _, obj := range registrationObjects(first, cluster, firstSAName) {
					g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj)).To(Succeed())
				}
			}).Should(Succeed())

			By("creating the token without changing the pending request or Cluster")
			createSettledToken(regNamespace, token.Name)
			grantRegistration(cr, "stale-token-sa-token")
			objectGone(first).Should(Succeed())
		})
	})

	Describe("manager-initiated registration", func() {
		It("registers an agent-created cluster with its import token after it gets kubeConfigSecret", func() {
			const clientID = "switched-client-id"

			first := newClusterRegistration(regNamespace, "first-request", clientID, "random-first", token.Name)
			Expect(k8sClient.Create(ctx, first)).To(Succeed())
			cluster, _ := grantRegistration(first, "first-sa-token")
			Expect(cluster.Annotations).To(HaveKeyWithValue(fleet.RegisteredTokenAnnotation, token.Name))

			Eventually(func(g Gomega) {
				current := &fleet.Cluster{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), current)).To(Succeed())
				current.Spec.KubeConfigSecret = "missing-kubeconfig"
				g.Expect(k8sClient.Update(ctx, current)).To(Succeed())
			}).Should(Succeed())
			importToken := createSettledToken(regNamespace,
				names.SafeConcatName(config.ImportTokenPrefix+cluster.Name))

			waitPastCreationSecond(first.CreationTimestamp)
			cr := newClusterRegistration(regNamespace, "imported-request", clientID, "random-imported", importToken.Name)
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			registered, _ := grantRegistration(cr, "imported-sa-token")
			Expect(registered.Name).To(Equal(cluster.Name))
			objectGone(first).Should(Succeed())
		})

		It("registers against the existing cluster and scopes the import token to the new registration Secret", func() {
			const clientID = "managed-client-id"

			cluster := newCluster(regNamespace, "managed-cluster")
			cluster.Spec.ClientID = clientID
			cluster.Spec.KubeConfigSecret = "missing-kubeconfig"
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
			Expect(registeredCluster(regNamespace, clientID).Name).To(Equal("managed-cluster"))

			importToken := createSettledToken(regNamespace,
				names.SafeConcatName(config.ImportTokenPrefix+cluster.Name))

			cr := newClusterRegistration(regNamespace, "managed-request", clientID, "random-g", "")
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			registered, _ := grantRegistration(cr, "managed-sa-token")
			Expect(registered.Name).To(Equal("managed-cluster"),
				"a registration for an existing client ID must reuse that cluster, not create a second one")

			importSAName := names.SafeConcatName(importToken.Name, string(importToken.UID))
			credRoleName := names.SafeConcatName(importSAName, "creds")

			role := &rbacv1.Role{}
			objectExists(&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{
				Namespace: systemRegistrationNamespace, Name: credRoleName}}).Should(Succeed())
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: systemRegistrationNamespace, Name: credRoleName}, role)).To(Succeed())
			Expect(role.Labels).To(HaveKeyWithValue(fleet.ManagedLabel, "true"))
			Expect(role.Rules).To(ConsistOf(rbacv1.PolicyRule{
				Verbs:         []string{"get"},
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: []string{credentialSecretName(cr)},
			}))

			binding := &rbacv1.RoleBinding{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: systemRegistrationNamespace, Name: credRoleName}, binding)).To(Succeed())
			Expect(binding.Subjects).To(ConsistOf(rbacv1.Subject{
				Kind:      "ServiceAccount",
				Name:      importSAName,
				Namespace: regNamespace,
			}))
		})
	})

	Describe("cluster labels", func() {
		It("copies the registration's cluster labels, dropping the ones an agent may not assert", func() {
			cr := newClusterRegistration(regNamespace, "labelled-request", "labelled-client-id", "random-h", token.Name)
			cr.Spec.ClusterLabels = map[string]string{
				"env":                        "test",
				fleet.CreatedByAgentPodLabel: "agent-pod",
				"management.cattle.io/cluster-display-name": "spoofed",
				"fleet.cattle.io/spoofed":                   "spoofed",
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			cluster := registeredCluster(regNamespace, "labelled-client-id")
			Expect(cluster.Labels).To(Equal(map[string]string{
				"env":                        "test",
				fleet.CreatedByAgentPodLabel: "agent-pod",
				fleet.ClusterAnnotation:      cluster.Name,
			}), "labels under the Rancher and Fleet prefixes are trusted for targeting, so an agent must not be able to set them")

			grantRegistration(cr, "labelled-sa-token")
		})

		It("ignores the registration's cluster labels when the global config disables copying them", func() {
			// Production reaches this state through the ConfigMap watch, which
			// calls config.SetAndTrigger. Setting the global directly keeps the
			// spec independent of that watch; nothing here depends on the
			// change callbacks the watch would also fire.
			previous := config.Get()
			ignoring := *previous
			ignoring.IgnoreClusterRegistrationLabels = true
			config.Set(&ignoring)
			DeferCleanup(func() { config.Set(previous) })

			cr := newClusterRegistration(regNamespace, "ignored-labels-request", "ignored-labels-client-id", "random-i", token.Name)
			cr.Spec.ClusterLabels = map[string]string{"env": "test"}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			cluster := registeredCluster(regNamespace, "ignored-labels-client-id")
			Expect(cluster.Labels).To(Equal(map[string]string{fleet.ClusterAnnotation: cluster.Name}))

			// A registration left half-way parks a controller worker for the
			// rest of the suite: the handler waits for the request service
			// account's token in an unbounded loop that only ends once the
			// token Secret is populated, which is what granting does.
			grantRegistration(cr, "ignored-labels-sa-token")
		})
	})

	Describe("a cluster whose generated name is already taken by another client ID", func() {
		It("refuses to register against it", func() {
			const clientID = "colliding-client-id"

			// The cluster name is a hash of the client ID, so a name that is
			// already taken by a different client ID can only be a collision.
			// Handing the registration that cluster would let one agent take
			// over another's bundle deployments.
			occupant := newCluster(regNamespace, names.SafeConcatName("cluster", names.KeyHash(clientID)))
			occupant.Spec.ClientID = "other-client-id"
			Expect(k8sClient.Create(ctx, occupant)).To(Succeed())

			// Wait for the cache to observe the occupant so this exercises
			// the cached lookup rather than the concurrent-creation path.
			Expect(registeredCluster(regNamespace, "other-client-id").Name).To(Equal(occupant.Name))

			cr := newClusterRegistration(regNamespace, "colliding-request", clientID, "random-m", token.Name)
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			consistently(func(g Gomega) {
				list := &fleet.ClusterList{}
				g.Expect(k8sClient.List(ctx, list, client.InNamespace(regNamespace))).To(Succeed())
				g.Expect(list.Items).To(HaveLen(1), "no second cluster may be created for the colliding name")
				g.Expect(list.Items[0].Spec.ClientID).To(Equal("other-client-id"))

				current := &fleet.ClusterRegistration{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), current)).To(Succeed())
				g.Expect(current.Status.Granted).To(BeFalse())
				g.Expect(current.Status.ClusterName).To(BeEmpty())
			}).Should(Succeed())
		})
	})

	Describe("registrations owned by another cluster manager", func() {
		It("creates neither a cluster nor any resources for them", func() {
			cr := newClusterRegistration(regNamespace, "externally-managed-request", "externally-managed-client-id", "random-j", "")
			cr.Labels = map[string]string{fleet.ClusterManagementLabel: "custom-manager"}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Nothing is created for such a registration, so there is no
			// positive signal to wait on; the window stands in for one.
			consistently(func(g Gomega) {
				list := &fleet.ClusterList{}
				g.Expect(k8sClient.List(ctx, list, client.InNamespace(regNamespace))).To(Succeed())
				g.Expect(list.Items).To(BeEmpty())

				current := &fleet.ClusterRegistration{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), current)).To(Succeed())
				g.Expect(current.Status.Granted).To(BeFalse())
				g.Expect(current.Status.ClusterName).To(BeEmpty())
			}).Should(Succeed())
		})
	})

	Describe("re-registration", func() {
		It("records the token of the first granted registration on a pre-created Cluster", func() {
			const clientID = "pre-created-client-id"
			precreated := newCluster(regNamespace, "pre-created-cluster")
			precreated.Spec.ClientID = clientID
			Expect(k8sClient.Create(ctx, precreated)).To(Succeed())
			Expect(registeredCluster(regNamespace, clientID).Annotations).NotTo(HaveKey(fleet.RegisteredTokenAnnotation))

			first := newClusterRegistration(regNamespace, "first-request", clientID, "random-first", token.Name)
			Expect(k8sClient.Create(ctx, first)).To(Succeed())
			cluster, _ := grantRegistration(first, "first-sa-token")
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed())
			Expect(cluster.Annotations).To(HaveKeyWithValue(fleet.RegisteredTokenAnnotation, token.Name))
		})

		It("retains the recovered token binding after granted registration history is removed", func() {
			const clientID = "legacy-client-id"
			first := newClusterRegistration(regNamespace, "first-request", clientID, "random-first", token.Name)
			Expect(k8sClient.Create(ctx, first)).To(Succeed())
			cluster, _ := grantRegistration(first, "first-sa-token")

			By("representing a Cluster registered before the annotation existed")
			Eventually(func(g Gomega) {
				current := &fleet.Cluster{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), current)).To(Succeed())
				delete(current.Annotations, fleet.RegisteredTokenAnnotation)
				g.Expect(k8sClient.Update(ctx, current)).To(Succeed())
			}).Should(Succeed())

			second := newClusterRegistration(regNamespace, "second-request", clientID, "random-second", token.Name)
			Expect(k8sClient.Create(ctx, second)).To(Succeed())
			saName := names.SafeConcatName(second.Name, string(second.UID))
			objectExists(&corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Namespace: cluster.Status.Namespace, Name: saName,
			}}).Should(Succeed())
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed())
			Expect(cluster.Annotations).To(HaveKeyWithValue(fleet.RegisteredTokenAnnotation, token.Name))

			By("removing history while the replacement has not yet been granted")
			Expect(k8sClient.Delete(ctx, first)).To(Succeed())
			objectGone(first).Should(Succeed())
			selectedToken := createSettledToken(regNamespace, "selected-token")
			third := newClusterRegistration(regNamespace, "third-request", clientID, "random-third", selectedToken.Name)
			Expect(k8sClient.Create(ctx, third)).To(Succeed())
			consistently(func(g Gomega) {
				current := &fleet.ClusterRegistration{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(third), current)).To(Succeed())
				g.Expect(current.Status.Granted).To(BeFalse())
				g.Expect(current.OwnerReferences).To(BeEmpty())
				currentCluster := &fleet.Cluster{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), currentCluster)).To(Succeed())
				g.Expect(currentCluster.Annotations).To(HaveKeyWithValue(fleet.RegisteredTokenAnnotation, token.Name))
			}).Should(Succeed())

			grantRegistration(second, "second-sa-token")
		})

		It("resumes a blocked registration when token changes are enabled through the ConfigMap", func() {
			const clientID = "config-client-id"
			first := newClusterRegistration(regNamespace, "first-request", clientID, "random-first", token.Name)
			Expect(k8sClient.Create(ctx, first)).To(Succeed())
			cluster, _ := grantRegistration(first, "first-sa-token")
			selectedToken := createSettledToken(regNamespace, "selected-token")
			waitPastCreationSecond(first.CreationTimestamp)
			second := newClusterRegistration(regNamespace, "second-request", clientID, "random-second", selectedToken.Name)
			Expect(k8sClient.Create(ctx, second)).To(Succeed())

			Eventually(func(g Gomega) {
				events := &corev1.EventList{}
				g.Expect(k8sClient.List(ctx, events, client.InNamespace(regNamespace))).To(Succeed())
				g.Expect(events.Items).To(ContainElement(And(
					HaveField("InvolvedObject.Name", cluster.Name),
					HaveField("Reason", "RegistrationTokenMismatch"),
				)))
			}).Should(Succeed())
			consistently(func(g Gomega) {
				current := &fleet.ClusterRegistration{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(second), current)).To(Succeed())
				g.Expect(current.Status.Granted).To(BeFalse())
				g.Expect(current.OwnerReferences).To(BeEmpty())
			}).Should(Succeed())

			previous := config.Get()
			cmKey := types.NamespacedName{Namespace: systemNamespace, Name: config.ManagerConfigName}
			original := &corev1.ConfigMap{}
			err := k8sClient.Get(ctx, cmKey, original)
			wasAbsent := apierrors.IsNotFound(err)
			if !wasAbsent {
				Expect(err).NotTo(HaveOccurred())
			}
			DeferCleanup(func() {
				Eventually(func(g Gomega) {
					current := &corev1.ConfigMap{}
					g.Expect(k8sClient.Get(ctx, cmKey, current)).To(Succeed())
					current.Data = newConfigMap(systemNamespace, config.ManagerConfigName, previous).Data
					g.Expect(k8sClient.Update(ctx, current)).To(Succeed())
				}).Should(Succeed())
				Eventually(config.Get).Should(Equal(previous))
				if wasAbsent {
					Expect(k8sClient.Delete(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
						Namespace: cmKey.Namespace, Name: cmKey.Name,
					}})).To(Succeed())
					objectGone(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
						Namespace: cmKey.Namespace, Name: cmKey.Name,
					}}).Should(Succeed())
				}
			})

			enabled := *previous
			enabled.AllowClusterRegistrationTokenChange = true
			cm := newConfigMap(systemNamespace, config.ManagerConfigName, &enabled)
			if wasAbsent {
				Expect(k8sClient.Create(ctx, cm)).To(Succeed())
			} else {
				original.Data = cm.Data
				Expect(k8sClient.Update(ctx, original)).To(Succeed())
			}
			Eventually(func() bool { return config.Get().AllowClusterRegistrationTokenChange }).Should(BeTrue())

			registered, _ := grantRegistration(second, "second-sa-token")
			Expect(registered.Name).To(Equal(cluster.Name))
			objectGone(first).Should(Succeed())
		})

		It("supersedes the older registration for the same client ID and removes everything it was granted", func() {
			const clientID = "re-registering-client-id"

			first := newClusterRegistration(regNamespace, "first-request", clientID, "random-first", token.Name)
			Expect(k8sClient.Create(ctx, first)).To(Succeed())
			cluster, firstSAName := grantRegistration(first, "first-sa-token")

			firstObjects := registrationObjects(first, cluster, firstSAName)
			for _, obj := range firstObjects {
				objectExists(obj).Should(Succeed())
			}

			waitPastCreationSecond(first.CreationTimestamp)
			second := newClusterRegistration(regNamespace, "second-request", clientID, "random-second", token.Name)
			Expect(k8sClient.Create(ctx, second)).To(Succeed())
			Expect(second.CreationTimestamp.Time).To(BeTemporally(">", first.CreationTimestamp.Time),
				"a registration only supersedes older ones, so the second must be measurably newer")

			secondCluster, secondSAName := grantRegistration(second, "second-sa-token")
			Expect(secondCluster.Name).To(Equal(cluster.Name))

			By("deleting the superseded registration")
			objectGone(first).Should(Succeed())

			By("pruning everything the superseded registration owned")
			for _, obj := range firstObjects {
				objectGone(obj).Should(Succeed())
			}

			By("leaving the current registration's own resources in place")
			for _, obj := range registrationObjects(second, secondCluster, secondSAName) {
				objectExists(obj).Should(Succeed())
			}
		})

		It("does not touch a granted registration's resources on later reconciles", func() {
			cr := newClusterRegistration(regNamespace, "settled-request", "settled-client-id", "random-k", token.Name)
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			cluster, saName := grantRegistration(cr, "settled-sa-token")

			credentials := &corev1.Secret{}
			key := types.NamespacedName{Namespace: systemRegistrationNamespace, Name: credentialSecretName(cr)}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, key, credentials)).To(Succeed())
			}).Should(Succeed())
			resourceVersion := credentials.ResourceVersion

			// Removing one owned object turns "the handler skips a granted
			// registration" into something observable. An unchanged
			// resourceVersion alone would not: applying an object that already
			// matches is a no-op write, so it looks the same either way.
			By("deleting one of the objects the registration owns")
			var probe client.Object
			survivors := []client.Object{}
			for _, obj := range registrationObjects(cr, cluster, saName) {
				if _, isContentBinding := obj.(*rbacv1.ClusterRoleBinding); isContentBinding {
					probe = obj
					continue
				}
				survivors = append(survivors, obj)
			}
			Expect(k8sClient.Delete(ctx, probe)).To(Succeed())
			objectGone(probe).Should(Succeed())

			By("forcing a reconcile of the granted registration")
			Eventually(func(g Gomega) {
				current := &fleet.ClusterRegistration{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), current)).To(Succeed())
				if current.Annotations == nil {
					current.Annotations = map[string]string{}
				}
				current.Annotations["test.fleet.cattle.io/nudge"] = strconv.FormatInt(time.Now().UnixNano(), 10)
				g.Expect(k8sClient.Update(ctx, current)).To(Succeed())
			}).Should(Succeed())

			absent := objectAbsent(probe)
			consistently(func(g Gomega) {
				absent(g)

				current := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, key, current)).To(Succeed())
				g.Expect(current.ResourceVersion).To(Equal(resourceVersion),
					"a granted registration is not re-applied, so its credentials must not be rewritten")

				reg := &fleet.ClusterRegistration{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cr), reg)).To(Succeed())
				g.Expect(reg.Status.Granted).To(BeTrue())
			}).Should(Succeed())

			By("leaving the objects it did not have to touch alone")
			for _, obj := range survivors {
				objectExists(obj).Should(Succeed())
			}
		})

		It("prunes everything a deleted registration was granted", func() {
			// Naming the token that registered it means the credential Secret
			// grant is created too, so pruning is asserted over the whole set.
			token := createSettledToken(regNamespace, "deleted-registration-token")

			cr := newClusterRegistration(regNamespace, "deleted-request", "deleted-client-id", "random-l", token.Name)
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			cluster, saName := grantRegistration(cr, "deleted-sa-token")

			objects := append(registrationObjects(cr, cluster, saName), credentialGrantObjects(cr)...)
			for _, obj := range objects {
				objectExists(obj).Should(Succeed())
			}

			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())
			objectGone(cr).Should(Succeed())

			for _, obj := range objects {
				objectGone(obj).Should(Succeed())
			}

			By("leaving the cluster it registered behind")
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), &fleet.Cluster{})).To(Succeed())
		})
	})
})
