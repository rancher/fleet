package agent_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// legacyFieldManager is the field manager the agent's pre-SSA
// read-modify-write namespace update recorded. The literal is pinned here
// instead of being imported from the deployer package on purpose: client-go
// derives this name from the binary, and so does Helm, and that collision is
// what the specs below guard against.
const legacyFieldManager = "fleetagent"

// These specs exercise the namespace metadata sync against a real API server,
// which - unlike the controller-runtime fake client - tracks server-side-apply
// field ownership. They are the authoritative check for issue #4564: foreign
// metadata is preserved, and a key that Fleet stops declaring is pruned.
var _ = Describe("BundleDeployment namespace metadata", Ordered, func() {
	var namespace string

	BeforeAll(func() {
		namespace = createNamespace()
	})

	// createTargetNamespace creates the release namespace up front, recorded
	// under the given field manager, so that a spec can set up the ownership
	// situation it wants to test. Fleet never creates the namespace itself.
	createTargetNamespace := func(name, fieldManager string, labels, annotations map[string]string) {
		GinkgoHelper()

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Labels:      labels,
			Annotations: annotations,
		}}
		Expect(k8sClient.Create(context.TODO(), ns, client.FieldOwner(fieldManager))).ToNot(HaveOccurred())
	}

	createBundleDeployment := func(name string, options v1alpha1.BundleDeploymentOptions) {
		GinkgoHelper()

		bd := &v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: clusterNS,
			},
			Spec: v1alpha1.BundleDeploymentSpec{
				DeploymentID: "v1",
				Options:      options,
			},
		}
		Expect(k8sClient.Create(context.TODO(), bd)).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_ = k8sClient.Delete(context.TODO(), bd)
		})
	}

	// updateOptions changes the namespace metadata a bundle declares. The
	// deployer re-applies on every reconcile, so no deployment ID bump is
	// needed to trigger a second sync.
	updateOptions := func(name string, labels, annotations map[string]string) {
		GinkgoHelper()

		Eventually(func(g Gomega) {
			bd := &v1alpha1.BundleDeployment{}
			g.Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Namespace: clusterNS, Name: name}, bd)).ToNot(HaveOccurred())
			bd.Spec.Options.NamespaceLabels = labels
			bd.Spec.Options.NamespaceAnnotations = annotations
			g.Expect(k8sClient.Update(context.TODO(), bd)).ToNot(HaveOccurred())
		}).Should(Succeed())
	}

	getTargetNamespace := func(name string) *corev1.Namespace {
		GinkgoHelper()

		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(context.TODO(), types.NamespacedName{Name: name}, ns)).ToNot(HaveOccurred())

		return ns
	}

	When("the namespace carries metadata owned by another actor", func() {
		It("preserves the foreign metadata and prunes keys the bundle stops declaring", func() {
			// A namespace with a foreign annotation, as if Rancher had moved
			// it into a Project. It is owned by a different field manager than
			// Fleet's.
			targetNamespace := namespace + "-foreign"
			createTargetNamespace(targetNamespace, "rancher", nil, map[string]string{
				"field.cattle.io/projectId": "p-abc123",
			})

			createBundleDeployment("ns-metadata-foreign", v1alpha1.BundleDeploymentOptions{
				DefaultNamespace:     targetNamespace,
				NamespaceLabels:      map[string]string{"team": "blue"},
				NamespaceAnnotations: map[string]string{"fleet-a": "1", "fleet-b": "2"},
			})

			Eventually(func(g Gomega) {
				ns := getTargetNamespace(targetNamespace)
				g.Expect(ns.Labels).To(HaveKeyWithValue("team", "blue"))
				g.Expect(ns.Annotations).To(HaveKeyWithValue("fleet-a", "1"))
				g.Expect(ns.Annotations).To(HaveKeyWithValue("fleet-b", "2"))
				g.Expect(ns.Annotations).To(HaveKeyWithValue("field.cattle.io/projectId", "p-abc123"))
			}).Should(Succeed())

			// Fleet drops fleet-b from the options. Because Fleet owns exactly
			// the keys it declares, fleet-b must be pruned, while the foreign
			// annotation and the still-declared keys remain.
			updateOptions("ns-metadata-foreign", map[string]string{"team": "blue"}, map[string]string{"fleet-a": "1"})

			Eventually(func(g Gomega) {
				ns := getTargetNamespace(targetNamespace)
				g.Expect(ns.Annotations).ToNot(HaveKey("fleet-b"))
			}).Should(Succeed())

			ns := getTargetNamespace(targetNamespace)
			Expect(ns.Annotations).To(HaveKeyWithValue("fleet-a", "1"))
			Expect(ns.Annotations).To(HaveKeyWithValue("field.cattle.io/projectId", "p-abc123"))
			Expect(ns.Labels).To(HaveKeyWithValue("team", "blue"))
		})
	})

	// Reproduces the in-place-upgrade scenario: a namespace whose Fleet
	// metadata was written by the old read-modify-write update, recorded under
	// the "fleetagent" manager, before Fleet switched to SSA. ForceOwnership on
	// the first apply gives the SSA manager co-ownership but does not, by
	// itself, remove the stale Update entry. Without the scoped managed-fields
	// migration, a key later dropped from the bundle would stay on the
	// namespace forever, because the stale entry still owns it.
	When("the namespace metadata was written by the pre-SSA agent", func() {
		It("migrates the declared keys so they can later be pruned", func() {
			targetNamespace := namespace + "-legacy"
			createTargetNamespace(targetNamespace, "cluster-admin", nil, nil)

			// Simulate the pre-SSA agent: a plain update (PUT), recorded under
			// the "fleetagent" manager, exactly like the old read-modify-write
			// path.
			ns := getTargetNamespace(targetNamespace)
			ns.Labels = map[string]string{"team": "blue"}
			ns.Annotations = map[string]string{"fleet-a": "1", "fleet-b": "2"}
			Expect(k8sClient.Update(context.TODO(), ns, client.FieldOwner(legacyFieldManager))).ToNot(HaveOccurred())

			createBundleDeployment("ns-metadata-legacy", v1alpha1.BundleDeploymentOptions{
				DefaultNamespace:     targetNamespace,
				NamespaceLabels:      map[string]string{"team": "blue"},
				NamespaceAnnotations: map[string]string{"fleet-a": "1", "fleet-b": "2", "fleet-c": "3"},
			})

			// fleet-c is not part of the legacy update, so its appearance marks
			// the first post-upgrade sync, and therefore the migration, as
			// done.
			Eventually(func(g Gomega) {
				ns := getTargetNamespace(targetNamespace)
				g.Expect(ns.Annotations).To(HaveKeyWithValue("fleet-c", "3"))
			}).Should(Succeed())

			// Now drop fleet-b: without the migration it would stay behind
			// forever, because the stale update entry still owned it.
			updateOptions(
				"ns-metadata-legacy",
				map[string]string{"team": "blue"},
				map[string]string{"fleet-a": "1", "fleet-c": "3"},
			)

			Eventually(func(g Gomega) {
				ns := getTargetNamespace(targetNamespace)
				g.Expect(ns.Annotations).ToNot(HaveKey("fleet-b"))
			}).Should(Succeed())

			ns = getTargetNamespace(targetNamespace)
			Expect(ns.Annotations).To(HaveKeyWithValue("fleet-a", "1"))
			Expect(ns.Labels).To(HaveKeyWithValue("team", "blue"))
		})
	})

	// Helm labels the release namespace it creates with "name: <namespace>".
	// Both client-go and Helm derive the field manager from the binary name, so
	// on a namespace that predates the SSA switch that label sits in the very
	// same "fleetagent" Update entry as the metadata the old read-modify-write
	// path wrote, and the two cannot be told apart. Migrating the entry
	// wholesale would make Fleet own Helm's label and prune it on the next
	// apply. See issue #4564.
	//
	// Helm's own namespace creation is switched off here: Helm 4 creates the
	// namespace with server-side apply, which would claim the "name" label for
	// an Apply entry of its own and so hide the ownership situation under test.
	When("the pre-SSA namespace also carries Helm's name label", func() {
		It("leaves the name label alone", func() {
			targetNamespace := namespace + "-helm-label"
			createTargetNamespace(targetNamespace, "cluster-admin", nil, nil)

			ns := getTargetNamespace(targetNamespace)
			ns.Labels = map[string]string{"name": targetNamespace, "team": "blue"}
			Expect(k8sClient.Update(context.TODO(), ns, client.FieldOwner(legacyFieldManager))).ToNot(HaveOccurred())

			createNamespaceDisabled := false
			createBundleDeployment("ns-metadata-helm-label", v1alpha1.BundleDeploymentOptions{
				DefaultNamespace: targetNamespace,
				CreateNamespace:  &createNamespaceDisabled,
				NamespaceLabels:  map[string]string{"team": "green"},
			})

			Eventually(func(g Gomega) {
				ns := getTargetNamespace(targetNamespace)
				g.Expect(ns.Labels).To(HaveKeyWithValue("team", "green"))
			}).Should(Succeed())

			Expect(getTargetNamespace(targetNamespace).Labels).To(
				HaveKeyWithValue("name", targetNamespace),
				"Helm's name label must survive the first apply",
			)

			// A second sync, to cover the case where the label is absorbed on
			// the first one and only pruned on the next.
			updateOptions("ns-metadata-helm-label", map[string]string{"team": "red"}, nil)

			Eventually(func(g Gomega) {
				ns := getTargetNamespace(targetNamespace)
				g.Expect(ns.Labels).To(HaveKeyWithValue("team", "red"))
			}).Should(Succeed())

			Expect(getTargetNamespace(targetNamespace).Labels).To(
				HaveKeyWithValue("name", targetNamespace),
				"Helm's name label must survive later applies",
			)
		})
	})

	When("the bundle declares a pod-security label", func() {
		It("applies and overwrites it like any other label", func() {
			targetNamespace := namespace + "-podsec"
			createTargetNamespace(targetNamespace, "cluster-admin", map[string]string{
				"pod-security.kubernetes.io/enforce": "restricted",
			}, nil)

			createBundleDeployment("ns-metadata-podsec", v1alpha1.BundleDeploymentOptions{
				DefaultNamespace: targetNamespace,
				NamespaceLabels: map[string]string{
					"pod-security.kubernetes.io/enforce": "privileged",
					"app-label":                          "value",
				},
			})

			Eventually(func(g Gomega) {
				ns := getTargetNamespace(targetNamespace)
				g.Expect(ns.Labels).To(HaveKeyWithValue("pod-security.kubernetes.io/enforce", "privileged"))
				g.Expect(ns.Labels).To(HaveKeyWithValue("app-label", "value"))
			}).Should(Succeed())
		})
	})
})
