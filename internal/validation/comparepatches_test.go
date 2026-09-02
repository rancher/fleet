package validation

import (
	"strings"
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
			assertErrorExact(t, InvalidJSONPointerError(c.value), c.want)
		})
	}
}

func TestComparePatchNames(t *testing.T) {
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
			name: "valid regex names",
			opts: withPatches(
				fleet.ComparePatch{Name: "my-service"},
				fleet.ComparePatch{Name: ".*-svc$"},
				fleet.ComparePatch{Name: "prefix-.*"},
			),
		},
		{
			name: "empty name matches everything",
			opts: withPatches(fleet.ComparePatch{Name: ""}),
		},
		{
			name: "invalid regex - unclosed bracket",
			opts: withPatches(fleet.ComparePatch{Name: "foo["}),
			want: `diff.comparePatches[0].name: invalid regular expression "foo["`,
		},
		{
			name: "invalid regex - unclosed paren",
			opts: withPatches(fleet.ComparePatch{Name: "a(b"}),
			want: `diff.comparePatches[0].name: invalid regular expression "a(b"`,
		},
		{
			name: "invalid regex - invalid repetition",
			opts: withPatches(fleet.ComparePatch{Name: "*x"}),
			want: `diff.comparePatches[0].name: invalid regular expression "*x"`,
		},
		{
			name: "indices identify the offending patch",
			path: "targets[1].",
			opts: withPatches(
				fleet.ComparePatch{Name: "valid"},
				fleet.ComparePatch{Name: "also-valid"},
				fleet.ComparePatch{Name: "(?P<invalid)"},
			),
			want: `targets[1].diff.comparePatches[2].name: invalid regular expression "(?P<invalid)"`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertError(t, ValidateComparePatchNames(c.path, c.opts), c.want)
		})
	}
}

func TestComparePatchOperations(t *testing.T) {
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
			name: "valid operations",
			opts: withPatches(fleet.ComparePatch{
				Operations: []fleet.Operation{
					{Op: "add", Path: "/spec/foo", Value: "bar"},
					{Op: "remove", Path: "/spec/clusterIP"},
					{Op: "replace", Path: "/spec/type", Value: "NodePort"},
					{Op: "test", Path: "/spec/replicas", Value: "3"},
					{Op: fleet.IgnoreOp},
				},
			}),
		},
		{
			name: "empty op is rejected",
			opts: withPatches(fleet.ComparePatch{
				Operations: []fleet.Operation{{Op: "", Path: "/spec/replicas"}},
			}),
			want: `diff.comparePatches[0].operations[0].op: unsupported operation ""`,
		},
		{
			name: "copy operation is not supported",
			opts: withPatches(fleet.ComparePatch{
				Operations: []fleet.Operation{{Op: "copy", Path: "/spec/foo"}},
			}),
			want: `diff.comparePatches[0].operations[0].op: unsupported operation "copy"`,
		},
		{
			name: "move operation is not supported",
			opts: withPatches(fleet.ComparePatch{
				Operations: []fleet.Operation{{Op: "move", Path: "/spec/foo"}},
			}),
			want: `diff.comparePatches[0].operations[0].op: unsupported operation "move"`,
		},
		{
			name: "bogus operation",
			opts: withPatches(fleet.ComparePatch{
				Operations: []fleet.Operation{{Op: "bogus", Path: "/spec/foo"}},
			}),
			want: `diff.comparePatches[0].operations[0].op: unsupported operation "bogus"`,
		},
		{
			name: "indices identify the offending operation",
			path: "targetCustomizations[0].",
			opts: withPatches(
				fleet.ComparePatch{
					Operations: []fleet.Operation{{Op: "remove", Path: "/spec/ok"}},
				},
				fleet.ComparePatch{
					Operations: []fleet.Operation{
						{Op: "remove", Path: "/spec/clusterIP"},
						{Op: "invalid"},
					},
				},
			),
			want: `targetCustomizations[0].diff.comparePatches[1].operations[1].op: unsupported operation "invalid"`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertError(t, ValidateComparePatchOperations(c.path, c.opts), c.want)
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
		t.Errorf("expected error containing %q, got none", want)
	case want != "" && !strings.Contains(err.Error(), want):
		t.Errorf("error = %q, want to contain %q", err.Error(), want)
	}
}

func assertErrorExact(t *testing.T, err error, want string) {
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
