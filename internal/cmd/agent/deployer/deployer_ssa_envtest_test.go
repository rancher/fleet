package deployer

import (
	"context"
	"os"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// TestSetNamespaceLabelsAndAnnotations_ServerSideApply exercises the namespace
// metadata sync against a real API server (envtest), which — unlike the
// controller-runtime fake client — tracks server-side-apply field ownership.
// This is the authoritative check for issue #4564: foreign metadata is
// preserved, and a key that Fleet stops declaring is pruned.
//
// It is skipped unless KUBEBUILDER_ASSETS points at envtest binaries (e.g. via
// `setup-envtest use -p env`).
func TestSetNamespaceLabelsAndAnnotations_ServerSideApply(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; skipping envtest-backed server-side apply test")
	}

	env := &envtest.Environment{}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("failed to start envtest: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	scheme := clientgoscheme.Scheme
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("failed to build client: %v", err)
	}

	ctx := context.Background()

	get := func(name string) *corev1.Namespace {
		t.Helper()
		ns := &corev1.Namespace{}
		if err := c.Get(ctx, types.NamespacedName{Name: name}, ns); err != nil {
			t.Fatalf("get namespace %q: %v", name, err)
		}
		return ns
	}

	t.Run("foreign metadata preserved and dropped keys pruned", func(t *testing.T) {
		// A namespace with a foreign annotation, as if Rancher had moved it into
		// a Project. It is owned by a different field manager than Fleet's.
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:        "issue-4564",
			Annotations: map[string]string{"field.cattle.io/projectId": "p-abc123"},
		}}
		if err := c.Create(ctx, ns, client.FieldOwner("rancher")); err != nil {
			t.Fatalf("create namespace: %v", err)
		}

		d := Deployer{client: c}

		// First sync: Fleet declares two annotations and a label.
		bd := &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
			Options: fleet.BundleDeploymentOptions{
				NamespaceLabels:      map[string]string{"team": "blue"},
				NamespaceAnnotations: map[string]string{"fleet-a": "1", "fleet-b": "2"},
			},
		}}
		if err := d.setNamespaceLabelsAndAnnotations(ctx, bd, "issue-4564/rel/1"); err != nil {
			t.Fatalf("first sync: %v", err)
		}

		got := get("issue-4564")
		if got.Annotations["field.cattle.io/projectId"] != "p-abc123" {
			t.Errorf("foreign projectId annotation not preserved: %v", got.Annotations)
		}
		if got.Annotations["fleet-a"] != "1" || got.Annotations["fleet-b"] != "2" {
			t.Errorf("Fleet annotations not applied: %v", got.Annotations)
		}
		if got.Labels["team"] != "blue" {
			t.Errorf("Fleet label not applied: %v", got.Labels)
		}

		// Second sync: Fleet drops fleet-b from the options. Because Fleet owns
		// exactly the keys it declares, fleet-b must be pruned, while the foreign
		// annotation and the still-declared keys remain.
		bd.Spec.Options.NamespaceAnnotations = map[string]string{"fleet-a": "1"}
		if err := d.setNamespaceLabelsAndAnnotations(ctx, bd, "issue-4564/rel/1"); err != nil {
			t.Fatalf("second sync: %v", err)
		}

		got = get("issue-4564")
		if _, ok := got.Annotations["fleet-b"]; ok {
			t.Errorf("dropped annotation fleet-b was not pruned: %v", got.Annotations)
		}
		if got.Annotations["fleet-a"] != "1" {
			t.Errorf("still-declared annotation fleet-a missing: %v", got.Annotations)
		}
		if got.Annotations["field.cattle.io/projectId"] != "p-abc123" {
			t.Errorf("foreign projectId annotation lost after prune: %v", got.Annotations)
		}
	})

	// Reproduces the in-place-upgrade scenario: a namespace whose Fleet
	// annotations were written by the old read-modify-write Update (recorded
	// under the "fleetagent" manager), before Fleet switched to SSA.
	// ForceOwnership on the first apply gives the SSA manager co-ownership but
	// does not, by itself, remove the stale Update entry. Without the scoped
	// managed-fields migration, a key later dropped from the bundle would stay
	// on the namespace forever, because the stale entry still owns it.
	t.Run("legacy update-owned annotation is migrated so it can later be pruned", func(t *testing.T) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "legacy-migration"}}
		if err := c.Create(ctx, ns, client.FieldOwner("cluster-admin")); err != nil {
			t.Fatalf("create namespace: %v", err)
		}

		// Simulate the pre-SSA agent: a plain Update (PUT), recorded under the
		// "fleetagent" manager, exactly like the old read-modify-write path.
		got := get("legacy-migration")
		got.Annotations = map[string]string{"fleet-a": "1", "fleet-b": "2"}
		got.Labels = map[string]string{"team": "blue"}
		if err := c.Update(ctx, got, client.FieldOwner(legacyNamespaceFieldManager)); err != nil {
			t.Fatalf("legacy update: %v", err)
		}

		got = get("legacy-migration")
		foundLegacy := false
		for _, mf := range got.ManagedFields {
			if mf.Manager == legacyNamespaceFieldManager && mf.Operation == metav1.ManagedFieldsOperationUpdate {
				foundLegacy = true
			}
		}
		if !foundLegacy {
			t.Fatalf("test setup did not produce a legacy fleetagent Update entry: %v", got.ManagedFields)
		}

		d := Deployer{client: c}
		bd := &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
			Options: fleet.BundleDeploymentOptions{
				NamespaceLabels:      map[string]string{"team": "blue"},
				NamespaceAnnotations: map[string]string{"fleet-a": "1", "fleet-b": "2"},
			},
		}}
		// First sync after the (simulated) upgrade: migrates the stale entry.
		if err := d.setNamespaceLabelsAndAnnotations(ctx, bd, "legacy-migration/rel/1"); err != nil {
			t.Fatalf("first sync: %v", err)
		}

		got = get("legacy-migration")
		for _, mf := range got.ManagedFields {
			if mf.Manager == legacyNamespaceFieldManager && mf.Operation == metav1.ManagedFieldsOperationUpdate {
				t.Errorf("legacy fleetagent Update entry was not migrated away: %v", got.ManagedFields)
			}
		}

		// Now drop fleet-b: without the migration this would stay behind
		// forever, because the stale entry still owned it.
		bd.Spec.Options.NamespaceAnnotations = map[string]string{"fleet-a": "1"}
		if err := d.setNamespaceLabelsAndAnnotations(ctx, bd, "legacy-migration/rel/1"); err != nil {
			t.Fatalf("second sync: %v", err)
		}

		got = get("legacy-migration")
		if _, ok := got.Annotations["fleet-b"]; ok {
			t.Errorf("dropped annotation fleet-b was not pruned after migration: %v", got.Annotations)
		}
		if got.Annotations["fleet-a"] != "1" {
			t.Errorf("still-declared annotation fleet-a missing: %v", got.Annotations)
		}
		if got.Labels["team"] != "blue" {
			t.Errorf("still-declared label team missing: %v", got.Labels)
		}
	})

	t.Run("pod-security labels are neither applied nor overwritten", func(t *testing.T) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   "podsec",
			Labels: map[string]string{"pod-security.kubernetes.io/enforce": "restricted"},
		}}
		if err := c.Create(ctx, ns, client.FieldOwner("cluster-admin")); err != nil {
			t.Fatalf("create namespace: %v", err)
		}

		d := Deployer{client: c}
		bd := &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
			Options: fleet.BundleDeploymentOptions{
				NamespaceLabels: map[string]string{
					"pod-security.kubernetes.io/enforce": "privileged",
					"app-label":                          "value",
				},
			},
		}}
		if err := d.setNamespaceLabelsAndAnnotations(ctx, bd, "podsec/rel/1"); err != nil {
			t.Fatalf("sync: %v", err)
		}

		got := get("podsec")
		if got.Labels["pod-security.kubernetes.io/enforce"] != "restricted" {
			t.Errorf("pod-security enforce label was overwritten: %v", got.Labels)
		}
		if got.Labels["app-label"] != "value" {
			t.Errorf("non-security label not applied: %v", got.Labels)
		}
	})
}
