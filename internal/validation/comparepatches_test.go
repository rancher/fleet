package validation

import (
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestIsJSONPointer(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{value: "/spec/replicas", want: true},
		{value: "/status", want: true},
		{value: "/", want: true}, // addresses the key "" — odd, but json-patch resolves it
		{value: "/metadata/annotations/deployment.kubernetes.io~1revision", want: true},
		{value: "/spec/a~0b", want: true},
		// json-patch leaves any "~" sequence other than "~0" and "~1" literal, so
		// these address keys really named "a~2b" and "trailing~". A stricter RFC 6901
		// check would reject them and break configurations which work today.
		{value: "/spec/a~2b", want: true},
		{value: "/spec/trailing~", want: true},

		{value: "", want: false},
		{value: "spec", want: false},
		{value: "spec.replicas", want: false},
		{value: "metadata/kind", want: false},
		{value: " /spec/replicas", want: false},
	}

	for _, c := range cases {
		t.Run(c.value, func(t *testing.T) {
			if got := IsJSONPointer(c.value); got != c.want {
				t.Errorf("IsJSONPointer(%q) = %v, want %v", c.value, got, c.want)
			}
		})
	}
}

func TestInvalidJSONPointerError(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "empty",
			value: "",
			want:  `invalid JSON pointer "": must not be empty, e.g. "/spec/replicas"`,
		},
		{
			name:  "kubernetes field path",
			value: "spec.replicas",
			want:  `invalid JSON pointer "spec.replicas": must start with "/", e.g. "/spec/replicas"`,
		},
		{
			name:  "single segment",
			value: "spec",
			want:  `invalid JSON pointer "spec": must start with "/", e.g. "/spec"`,
		},
		{
			// The hint may only convert dots where doing so really produces the
			// pointer the user meant. Here it does not: the key is one annotation
			// name, so the pointer is
			// "/metadata/annotations/deployment.kubernetes.io~1revision" and a hint of
			// "/metadata/annotations/deployment/kubernetes/io/revision" would be wrong
			// in a message whose entire purpose is to be copied.
			name:  "key containing a slash and dots",
			value: "metadata.annotations.deployment.kubernetes.io/revision",
			want:  `invalid JSON pointer "metadata.annotations.deployment.kubernetes.io/revision": must start with "/", e.g. "/spec/replicas"`,
		},
		{
			name:  "key containing a tilde",
			value: "spec.a~2b",
			want:  `invalid JSON pointer "spec.a~2b": must start with "/", e.g. "/spec/replicas"`,
		},
		{
			// A leading dot would convert to an empty first segment, giving the hint
			// "//spec/replicas" — which IsJSONPointer accepts, so a user copying it
			// would get a value fleet apply lets through and the agent then fails on
			// silently. The fixed hint is the only safe answer.
			name:  "leading dot",
			value: ".spec.replicas",
			want:  `invalid JSON pointer ".spec.replicas": must start with "/", e.g. "/spec/replicas"`,
		},
		{
			name:  "trailing dot",
			value: "spec.replicas.",
			want:  `invalid JSON pointer "spec.replicas.": must start with "/", e.g. "/spec/replicas"`,
		},
		{
			name:  "doubled dot",
			value: "spec..replicas",
			want:  `invalid JSON pointer "spec..replicas": must start with "/", e.g. "/spec/replicas"`,
		},
		{
			name:  "two segments addressing the document root",
			value: "metadata/kind",
			want:  `invalid JSON pointer "metadata/kind": must start with "/", e.g. "/spec/replicas"`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := InvalidJSONPointerError(c.value).Error(); got != c.want {
				t.Errorf("InvalidJSONPointerError(%q) = %q, want %q", c.value, got, c.want)
			}
		})
	}
}

func TestComparePatchOperationPaths(t *testing.T) {
	cases := []struct {
		name string
		path string
		opts *fleet.BundleDeploymentOptions
		want string // empty means no error
	}{
		{
			name: "no diff options",
			opts: &fleet.BundleDeploymentOptions{},
		},
		{
			name: "no compare patches",
			opts: &fleet.BundleDeploymentOptions{Diff: &fleet.DiffOptions{}},
		},
		{
			name: "valid paths",
			opts: withPatches(fleet.ComparePatch{
				Operations: []fleet.Operation{
					{Op: "remove", Path: "/spec/clusterIP"},
					{Op: "replace", Path: "/spec/a~2b", Value: "x"},
				},
			}),
		},
		{
			// An "ignore" operation drops the whole resource from the diff and its
			// path is never read, so whatever is in it cannot break anything.
			name: "ignore operation with an unusable path",
			opts: withPatches(fleet.ComparePatch{
				Operations: []fleet.Operation{
					{Op: fleet.IgnoreOp, Path: "spec.replicas"},
					{Op: fleet.IgnoreOp},
				},
			}),
		},
		{
			name: "kubernetes field path",
			opts: withPatches(fleet.ComparePatch{
				Operations: []fleet.Operation{{Op: "remove", Path: "spec.replicas"}},
			}),
			want: `diff.comparePatches[0].operations[0].path: invalid JSON pointer "spec.replicas": must start with "/", e.g. "/spec/replicas"`,
		},
		{
			name: "empty path",
			opts: withPatches(fleet.ComparePatch{
				Operations: []fleet.Operation{{Op: "remove"}},
			}),
			want: `diff.comparePatches[0].operations[0].path: invalid JSON pointer "": must not be empty, e.g. "/spec/replicas"`,
		},
		{
			// Two segments without a leading slash resolve, but to the root of the
			// document rather than to what the user wrote, so they are rejected too.
			name: "two segments addressing the document root",
			opts: withPatches(fleet.ComparePatch{
				Operations: []fleet.Operation{{Op: "replace", Path: "metadata/kind", Value: "Pod"}},
			}),
			want: `diff.comparePatches[0].operations[0].path: invalid JSON pointer "metadata/kind": must start with "/", e.g. "/spec/replicas"`,
		},
		{
			name: "indices identify the offending operation",
			path: "targets[2].",
			opts: withPatches(
				fleet.ComparePatch{
					Operations: []fleet.Operation{{Op: "remove", Path: "/spec/clusterIP"}},
				},
				fleet.ComparePatch{
					Operations: []fleet.Operation{
						{Op: "remove", Path: "/spec/ok"},
						{Op: "remove", Path: "spec.bad"},
					},
				},
			),
			want: `targets[2].diff.comparePatches[1].operations[1].path: invalid JSON pointer "spec.bad": must start with "/", e.g. "/spec/bad"`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertError(t, ValidateComparePatchOperationPaths(c.path, c.opts), c.want)
		})
	}
}

func TestComparePatchJSONPointers(t *testing.T) {
	cases := []struct {
		name string
		path string
		opts *fleet.BundleDeploymentOptions
		want string // empty means no error
	}{
		{
			name: "no diff options",
			opts: &fleet.BundleDeploymentOptions{},
		},
		{
			name: "no compare patches",
			opts: &fleet.BundleDeploymentOptions{Diff: &fleet.DiffOptions{}},
		},
		{
			name: "valid pointers",
			opts: withPatches(fleet.ComparePatch{
				JsonPointers: []string{
					"/spec/replicas",
					"/metadata/annotations/deployment.kubernetes.io~1revision",
					"/spec/trailing~",
				},
			}),
		},
		{
			// Unlike an operation path, a jsonPointers entry is never skipped: it
			// does not belong to an operation, so there is no "ignore" to skip it for.
			name: "checked next to an ignore operation",
			opts: withPatches(fleet.ComparePatch{
				Operations:   []fleet.Operation{{Op: fleet.IgnoreOp}},
				JsonPointers: []string{"spec.replicas"},
			}),
			want: `diff.comparePatches[0].jsonPointers[0]: invalid JSON pointer "spec.replicas": must start with "/", e.g. "/spec/replicas"`,
		},
		{
			name: "empty pointer",
			opts: withPatches(fleet.ComparePatch{JsonPointers: []string{""}}),
			want: `diff.comparePatches[0].jsonPointers[0]: invalid JSON pointer "": must not be empty, e.g. "/spec/replicas"`,
		},
		{
			name: "two segments addressing the document root",
			opts: withPatches(fleet.ComparePatch{JsonPointers: []string{"metadata/kind"}}),
			want: `diff.comparePatches[0].jsonPointers[0]: invalid JSON pointer "metadata/kind": must start with "/", e.g. "/spec/replicas"`,
		},
		{
			name: "indices identify the offending pointer",
			path: "targetCustomizations[1].",
			opts: withPatches(
				fleet.ComparePatch{JsonPointers: []string{"/spec/replicas"}},
				fleet.ComparePatch{JsonPointers: []string{"/spec/ok", "spec.bad"}},
			),
			want: `targetCustomizations[1].diff.comparePatches[1].jsonPointers[1]: invalid JSON pointer "spec.bad": must start with "/", e.g. "/spec/bad"`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertError(t, ValidateComparePatchJSONPointers(c.path, c.opts), c.want)
		})
	}
}

func withPatches(patches ...fleet.ComparePatch) *fleet.BundleDeploymentOptions {
	return &fleet.BundleDeploymentOptions{
		Diff: &fleet.DiffOptions{ComparePatches: patches},
	}
}

func assertError(t *testing.T, err error, want string) {
	t.Helper()

	switch {
	case want == "" && err != nil:
		t.Errorf("unexpected error: %v", err)
	case want != "" && err == nil:
		t.Errorf("expected error %q, got none", want)
	case want != "" && err.Error() != want:
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}
