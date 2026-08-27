package desiredset

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/objectset"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestNewNormalizers_DropsUnsupportedOperation pins the drop newNormalizers does
// for an operation validation.IsSupportedPatchOp rejects. Without it the bad
// operation is marshalled into a JSON patch of its own, json-patch fails to apply
// it, and JSONPatchNormalizer's applyPatches returns nil for the whole object —
// discarding the good operation next to it as well. So the assertion is that the
// good operation still takes effect: /spec/clusterIP is really gone.
func TestNewNormalizers_DropsUnsupportedOperation(t *testing.T) {
	bd := &fleet.BundleDeployment{
		Spec: fleet.BundleDeploymentSpec{
			Options: fleet.BundleDeploymentOptions{
				Diff: &fleet.DiffOptions{
					ComparePatches: []fleet.ComparePatch{
						{
							APIVersion: "v1",
							Kind:       "Service",
							Namespace:  "test-ns",
							Name:       "my-svc",
							Operations: []fleet.Operation{
								{Op: "remove", Path: "/spec/clusterIP"},
								{Op: "bogus"},
							},
						},
					},
				},
			},
		},
	}

	svc := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]any{
				"name":      "my-svc",
				"namespace": "test-ns",
			},
			"spec": map[string]any{
				"clusterIP": "10.43.0.1",
				"type":      "ClusterIP",
			},
		},
	}

	live := objectset.ObjectByGVK{}

	norm, err := newNormalizers(logr.Discard(), live, bd)
	if err != nil {
		t.Fatalf("newNormalizers() unexpected error: %v", err)
	}

	if err := norm.Normalize(svc); err != nil {
		t.Fatalf("Normalize() unexpected error: %v", err)
	}

	if _, found, err := unstructured.NestedString(svc.Object, "spec", "clusterIP"); err != nil {
		t.Fatalf("reading spec.clusterIP: %v", err)
	} else if found {
		t.Error("spec.clusterIP is still set: the unsupported operation next to it discarded the whole patch")
	}

	// The rest of the object has to survive, so that the check above cannot pass
	// just because normalization wiped everything.
	if svcType, found, err := unstructured.NestedString(svc.Object, "spec", "type"); err != nil {
		t.Fatalf("reading spec.type: %v", err)
	} else if !found || svcType != "ClusterIP" {
		t.Errorf("spec.type = %q (found %v), want %q: normalization dropped more than the patched field", svcType, found, "ClusterIP")
	}
}

// TestNewNormalizers_DropsInvalidOperationPath pins the drop newNormalizers does
// for an operation path validation.IsJSONPointer rejects. Without it the bad
// operation is marshalled into a JSON patch of its own, json-patch fails to
// resolve the path, and JSONPatchNormalizer's applyPatches returns nil for the
// whole object — discarding the good operation next to it as well. So the
// assertion is that the good operation still takes effect: /spec/clusterIP is
// really gone.
func TestNewNormalizers_DropsInvalidOperationPath(t *testing.T) {
	bd := &fleet.BundleDeployment{
		Spec: fleet.BundleDeploymentSpec{
			Options: fleet.BundleDeploymentOptions{
				Diff: &fleet.DiffOptions{
					ComparePatches: []fleet.ComparePatch{
						{
							APIVersion: "v1",
							Kind:       "Service",
							Namespace:  "test-ns",
							Name:       "my-svc",
							Operations: []fleet.Operation{
								{Op: "remove", Path: "/spec/clusterIP"},
								{Op: "remove", Path: "spec.type"},
							},
						},
					},
				},
			},
		},
	}

	svc := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]any{
				"name":      "my-svc",
				"namespace": "test-ns",
			},
			"spec": map[string]any{
				"clusterIP": "10.43.0.1",
				"type":      "ClusterIP",
			},
		},
	}

	live := objectset.ObjectByGVK{}

	norm, err := newNormalizers(logr.Discard(), live, bd)
	if err != nil {
		t.Fatalf("newNormalizers() unexpected error: %v", err)
	}

	if err := norm.Normalize(svc); err != nil {
		t.Fatalf("Normalize() unexpected error: %v", err)
	}

	if _, found, err := unstructured.NestedString(svc.Object, "spec", "clusterIP"); err != nil {
		t.Fatalf("reading spec.clusterIP: %v", err)
	} else if found {
		t.Error("spec.clusterIP is still set: the unusable path next to it discarded the whole patch")
	}

	// The field the unusable path aimed at has to survive: dropping the operation
	// must not turn into applying it.
	if svcType, found, err := unstructured.NestedString(svc.Object, "spec", "type"); err != nil {
		t.Fatalf("reading spec.type: %v", err)
	} else if !found || svcType != "ClusterIP" {
		t.Errorf("spec.type = %q (found %v), want %q: normalization dropped more than the patched field", svcType, found, "ClusterIP")
	}
}

// TestNewNormalizers_DropsInvalidJSONPointer pins the drop newNormalizers does for
// a jsonPointers entry validation.IsJSONPointer rejects, before the entry reaches
// the vendored ignore normalizer. The user-visible diff is the same either way —
// that normalizer fails each pointer on its own and carries on, and a pointer it
// does resolve to the wrong place is normalized out of live, desired and original
// alike — so what this pins is the *visibility*: it reports the failure at V(1) info
// level, which the agent's default verbosity filters out entirely, and the drop
// exists so the same failure is reported as an error instead. The valid pointer
// next to it still has to take effect.
func TestNewNormalizers_DropsInvalidJSONPointer(t *testing.T) {
	var logged []string
	logger := funcr.New(func(prefix, args string) { logged = append(logged, args) }, funcr.Options{})

	bd := &fleet.BundleDeployment{
		Spec: fleet.BundleDeploymentSpec{
			Options: fleet.BundleDeploymentOptions{
				Diff: &fleet.DiffOptions{
					ComparePatches: []fleet.ComparePatch{
						{
							APIVersion:   "v1",
							Kind:         "Service",
							Namespace:    "test-ns",
							Name:         "my-svc",
							JsonPointers: []string{"spec.type", "/spec/clusterIP"},
						},
					},
				},
			},
		},
	}

	svc := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]any{
				"name":      "my-svc",
				"namespace": "test-ns",
			},
			"spec": map[string]any{
				"clusterIP": "10.43.0.1",
				"type":      "ClusterIP",
			},
		},
	}

	live := objectset.ObjectByGVK{}

	norm, err := newNormalizers(logger, live, bd)
	if err != nil {
		t.Fatalf("newNormalizers() unexpected error: %v", err)
	}

	if err := norm.Normalize(svc); err != nil {
		t.Fatalf("Normalize() unexpected error: %v", err)
	}

	if _, found, err := unstructured.NestedString(svc.Object, "spec", "clusterIP"); err != nil {
		t.Fatalf("reading spec.clusterIP: %v", err)
	} else if found {
		t.Error("spec.clusterIP is still set: the unusable pointer next to it took the valid one down")
	}

	if svcType, found, err := unstructured.NestedString(svc.Object, "spec", "type"); err != nil {
		t.Fatalf("reading spec.type: %v", err)
	} else if !found || svcType != "ClusterIP" {
		t.Errorf("spec.type = %q (found %v), want %q: normalization dropped more than the ignored field", svcType, found, "ClusterIP")
	}

	// The whole point of dropping the entry here rather than leaving it to the
	// vendored normalizer: the user has to be told.
	if !slices.ContainsFunc(logged, func(line string) bool {
		return strings.Contains(line, "invalid JSON pointer") && strings.Contains(line, "spec.type")
	}) {
		t.Errorf("the unusable pointer was discarded without being logged as an error; logged: %v", logged)
	}
}
