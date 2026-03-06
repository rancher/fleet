package cluster

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// agentSchedulingCustomizationNullable retrieves the CRD from the API server
// and returns whether agentSchedulingCustomization carries nullable: true in
// the served OpenAPI schema.  This is the exact property that clients such as
// the Rancher UI read when deciding whether to display the field as commented
// out (nullable / optional) or as an active object section.
//
// See https://github.com/rancher/rancher/issues/53781.
func agentSchedulingCustomizationNullable() (bool, error) {
	// Build a dedicated client that has the CRD type in its scheme.  The
	// shared k8sClient uses kubectl's default scheme which does not include
	// apiextensionsv1, so reading a CustomResourceDefinition through it
	// would fail with "no kind is registered".
	extScheme := runtime.NewScheme()
	if err := apiextensionsv1.AddToScheme(extScheme); err != nil {
		return false, err
	}
	crdClient, err := client.New(cfg, client.Options{Scheme: extScheme})
	if err != nil {
		return false, err
	}

	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := crdClient.Get(ctx, types.NamespacedName{Name: "clusters.fleet.cattle.io"}, crd); err != nil {
		return false, err
	}
	for _, ver := range crd.Spec.Versions {
		if ver.Name != "v1alpha1" {
			continue
		}
		if ver.Schema == nil || ver.Schema.OpenAPIV3Schema == nil {
			return false, nil
		}
		specProps := ver.Schema.OpenAPIV3Schema.Properties["spec"]
		fieldSchema, ok := specProps.Properties["agentSchedulingCustomization"]
		if !ok {
			return false, nil
		}
		return fieldSchema.Nullable, nil
	}
	return false, nil
}

// These tests verify the expected API behaviour of the agentSchedulingCustomization
// field.
//
// The CRD schema for agentSchedulingCustomization was missing nullable: true.
// The Rancher UI (and other OpenAPI-schema-aware clients) use that marker to
// decide whether a field is truly optional:
//
//   - nullable: true → field is omitted / shown commented-out when not set
//   - absent         → field is treated as a required-to-have-value object
//     and shown uncommented, causing the UI to render and
//     serialize an empty agentSchedulingCustomization: {}
//     even when the user never configured it
var _ = Describe("Cluster agentSchedulingCustomization nullability", func() {
	newNamespace := func() string {
		ns, err := utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		obj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
		Expect(k8sClient.Create(ctx, obj)).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, obj)).ToNot(HaveOccurred()) })
		return ns
	}

	It("is marked nullable in the CRD OpenAPI schema served by the API server", func() {
		nullable, err := agentSchedulingCustomizationNullable()
		Expect(err).ToNot(HaveOccurred())
		Expect(nullable).To(BeTrue(),
			"agentSchedulingCustomization must have nullable: true in the CRD schema "+
				"so OpenAPI-aware clients (e.g. Rancher UI) treat it as optional and "+
				"do not render or persist an empty agentSchedulingCustomization: {} "+
				"when the user has not configured it")
	})

	It("is nil when not set on a new cluster", func() {
		namespace = newNamespace()
		cluster := &v1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-nullable-absent",
				Namespace: namespace,
			},
		}
		Expect(k8sClient.Create(ctx, cluster)).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, cluster)).ToNot(HaveOccurred())
		})

		fetched := &v1alpha1.Cluster{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cluster.Name, Namespace: namespace}, fetched)).ToNot(HaveOccurred())
		Expect(fetched.Spec.AgentSchedulingCustomization).To(BeNil(),
			"agentSchedulingCustomization should be absent, not serialised as an empty object")
	})

	It("can be cleared to nil via JSON merge patch after being set", func() {
		// Simulate the state from the issue: agentSchedulingCustomization: {}
		// was written by a previous client; verify it can be removed via a null
		// merge patch, which is how kubectl and the Rancher UI clear fields.
		namespace = newNamespace()
		cluster := &v1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-nullable-clear",
				Namespace: namespace,
			},
			Spec: v1alpha1.ClusterSpec{
				AgentSchedulingCustomization: &v1alpha1.AgentSchedulingCustomization{
					PriorityClass: &v1alpha1.PriorityClassSpec{Value: 100},
				},
			},
		}
		Expect(k8sClient.Create(ctx, cluster)).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, cluster)).ToNot(HaveOccurred())
		})

		fetched := &v1alpha1.Cluster{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cluster.Name, Namespace: namespace}, fetched)).ToNot(HaveOccurred())
		Expect(fetched.Spec.AgentSchedulingCustomization).ToNot(BeNil())

		nullPatch := []byte(`{"spec":{"agentSchedulingCustomization":null}}`)
		Expect(k8sClient.Patch(ctx, fetched, client.RawPatch(types.MergePatchType, nullPatch))).ToNot(HaveOccurred(),
			"clearing agentSchedulingCustomization via null merge patch must succeed (requires nullable: true in CRD)")

		cleared := &v1alpha1.Cluster{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cluster.Name, Namespace: namespace}, cleared)).ToNot(HaveOccurred())
		Expect(cleared.Spec.AgentSchedulingCustomization).To(BeNil(),
			"agentSchedulingCustomization should be nil after being cleared via null merge patch")
	})

	It("is not normalized when PriorityClass is set", func() {
		// A non-empty agentSchedulingCustomization (PriorityClass configured)
		// must not be patched to nil by the controller.
		namespace = newNamespace()
		cluster := &v1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-nullable-priorityclass",
				Namespace: namespace,
			},
			Spec: v1alpha1.ClusterSpec{
				AgentSchedulingCustomization: &v1alpha1.AgentSchedulingCustomization{
					PriorityClass: &v1alpha1.PriorityClassSpec{Value: 100},
				},
			},
		}
		Expect(k8sClient.Create(ctx, cluster)).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, cluster)).ToNot(HaveOccurred())
		})

		// Wait for the finalizer to appear — this confirms the controller has
		// completed at least one reconcile and reached the normalization step.
		Eventually(func(g Gomega) {
			fetched := &v1alpha1.Cluster{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cluster.Name, Namespace: namespace}, fetched)).ToNot(HaveOccurred())
			g.Expect(fetched.Finalizers).To(ContainElement("fleet.cattle.io/cluster-finalizer"))
		}).Should(Succeed())

		Consistently(func(g Gomega) {
			fetched := &v1alpha1.Cluster{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cluster.Name, Namespace: namespace}, fetched)).ToNot(HaveOccurred())
			g.Expect(fetched.Spec.AgentSchedulingCustomization).ToNot(BeNil(),
				"controller must not normalize a non-empty agentSchedulingCustomization")
			g.Expect(fetched.Spec.AgentSchedulingCustomization.PriorityClass).ToNot(BeNil())
			g.Expect(fetched.Spec.AgentSchedulingCustomization.PriorityClass.Value).To(Equal(100))
		}).Should(Succeed())
	})

	It("is not normalized when PodDisruptionBudget is set", func() {
		// A non-empty agentSchedulingCustomization (PodDisruptionBudget configured)
		// must not be patched to nil by the controller.
		namespace = newNamespace()
		cluster := &v1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-nullable-pdb",
				Namespace: namespace,
			},
			Spec: v1alpha1.ClusterSpec{
				AgentSchedulingCustomization: &v1alpha1.AgentSchedulingCustomization{
					PodDisruptionBudget: &v1alpha1.PodDisruptionBudgetSpec{MinAvailable: "1"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, cluster)).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, cluster)).ToNot(HaveOccurred())
		})

		// Wait for the finalizer to appear — this confirms the controller has
		// completed at least one reconcile and reached the normalization step.
		Eventually(func(g Gomega) {
			fetched := &v1alpha1.Cluster{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cluster.Name, Namespace: namespace}, fetched)).ToNot(HaveOccurred())
			g.Expect(fetched.Finalizers).To(ContainElement("fleet.cattle.io/cluster-finalizer"))
		}).Should(Succeed())

		Consistently(func(g Gomega) {
			fetched := &v1alpha1.Cluster{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cluster.Name, Namespace: namespace}, fetched)).ToNot(HaveOccurred())
			g.Expect(fetched.Spec.AgentSchedulingCustomization).ToNot(BeNil(),
				"controller must not normalize a non-empty agentSchedulingCustomization")
			g.Expect(fetched.Spec.AgentSchedulingCustomization.PodDisruptionBudget).ToNot(BeNil())
			g.Expect(fetched.Spec.AgentSchedulingCustomization.PodDisruptionBudget.MinAvailable).To(Equal("1"))
		}).Should(Succeed())
	})

	It("is normalized from empty struct to nil by the controller", func() {
		// This simulates what Rancher's provisioning layer does: it initializes
		// AgentSchedulingCustomization as a non-nil empty struct, which gets
		// serialized as agentSchedulingCustomization: {} in etcd.  The Fleet
		// cluster controller should detect the empty struct and clear it to nil
		// so OpenAPI-aware UIs do not show it uncommented.
		namespace = newNamespace()
		cluster := &v1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-nullable-normalize",
				Namespace: namespace,
			},
			Spec: v1alpha1.ClusterSpec{
				AgentSchedulingCustomization: &v1alpha1.AgentSchedulingCustomization{},
			},
		}
		Expect(k8sClient.Create(ctx, cluster)).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(k8sClient.Delete(ctx, cluster)).ToNot(HaveOccurred())
		})

		Eventually(func(g Gomega) {
			normalized := &v1alpha1.Cluster{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cluster.Name, Namespace: namespace}, normalized)).ToNot(HaveOccurred())
			g.Expect(normalized.Spec.AgentSchedulingCustomization).To(BeNil(),
				"controller should normalize agentSchedulingCustomization: {} to nil")
		}).Should(Succeed())
	})
})
