package desiredset_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	jsonpatch "github.com/evanphx/json-patch"

	"github.com/rancher/fleet/internal/validation"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// rejectedPatchOps are values which must never be accepted: unknown names, wrong
// case and stray whitespace, plus the two JSON Patch operations Fleet cannot encode.
// json-patch implements "copy" and "move", but fleet.Operation has no "from"
// field, so neither can ever be handed to it in a form it accepts.
var rejectedPatchOps = []string{"", "Remove", "remove ", "bogus", "delete", "copy", "move"}

// TestNormalizers_AgreesWithValidation pins validation.IsSupportedPatchOp, which
// fleet apply rejects a comparePatch operation with, to what the agent can really
// apply. It drives the accepted half from validation.SupportedPatchOps, so adding
// an operation to the predicate which the agent cannot apply fails here, and it
// goes through the same fleet.Operation marshalling newNormalizers does, so an
// operation which json-patch implements but fleet.Operation cannot express — one
// needing a "from" — counts as unsupported, the way it really behaves.
func TestNormalizers_AgreesWithValidation(t *testing.T) {
	for _, op := range validation.SupportedPatchOps() {
		t.Run(fmt.Sprintf("accepted op %q", op), func(t *testing.T) {
			if slices.Contains(rejectedPatchOps, op) {
				t.Fatalf("IsSupportedPatchOp(%q) = true, but it is listed as an operation which must be rejected", op)
			}

			if op == fleet.IgnoreOp {
				// "ignore" is Fleet's own extension: Diff handles it and it never
				// reaches json-patch, which is why it has to be accepted here and
				// is expected to be unknown to the library.
				if err := applyAsJSONPatch(t, op); err == nil {
					t.Errorf("json-patch now applies %q; Diff intercepts it before json-patch sees it, revisit that", op)
				}
				return
			}

			if err := applyAsJSONPatch(t, op); err != nil {
				t.Errorf(
					"IsSupportedPatchOp(%q) = true, but the agent cannot apply it: %v",
					op, err,
				)
			}
		})
	}

	for _, op := range rejectedPatchOps {
		t.Run(fmt.Sprintf("rejected op %q", op), func(t *testing.T) {
			if validation.IsSupportedPatchOp(op) {
				t.Errorf("IsSupportedPatchOp(%q) = true, but the agent cannot apply it", op)
			}

			if err := applyAsJSONPatch(t, op); err == nil {
				t.Errorf("json-patch applied %q; it is rejected by the validator, so the two disagree", op)
			}
		})
	}
}

// applyAsJSONPatch runs op through the agent's path: it marshals a real
// fleet.Operation exactly as newNormalizers does, then decodes and applies the
// result the way JSONPatchNormalizer does. It returns the error the agent would
// hit, or nil if the operation applies.
func applyAsJSONPatch(t *testing.T, op string) error {
	t.Helper()

	// Every field fleet.Operation has is set, so that a supported operation
	// applies cleanly to the document below: "test" compares /a against its
	// actual value.
	patchData, err := json.Marshal([]any{fleet.Operation{Op: op, Path: "/a", Value: "b"}})
	if err != nil {
		t.Fatalf("marshalling an operation %q: %v", op, err)
	}

	patch, err := jsonpatch.DecodePatch(patchData)
	if err != nil {
		return err
	}

	_, err = patch.Apply([]byte(`{"a":"b"}`))

	return err
}

// acceptedJSONPointers are pointers validation.IsJSONPointer must keep accepting,
// each with a document it addresses and what removing that pointer has to leave
// behind. The document is the point: it pins that json-patch resolves the pointer
// to the key the user wrote, not merely that the patch applies without an error.
var acceptedJSONPointers = []struct {
	pointer string
	doc     string
	want    string
}{
	{
		pointer: "/spec/replicas",
		doc:     `{"spec":{"replicas":3,"paused":true}}`,
		want:    `{"spec":{"paused":true}}`,
	},
	{
		// A single segment addresses a top-level key.
		pointer: "/status",
		doc:     `{"spec":{},"status":{"phase":"Running"}}`,
		want:    `{"spec":{}}`,
	},
	{
		// "~1" is the RFC 6901 escape for "/", so this is one annotation key
		// containing a slash, not four segments.
		pointer: "/metadata/annotations/deployment.kubernetes.io~1revision",
		doc:     `{"metadata":{"annotations":{"deployment.kubernetes.io/revision":"1","other":"x"}}}`,
		want:    `{"metadata":{"annotations":{"other":"x"}}}`,
	},
	{
		// "~0" is the escape for "~".
		pointer: "/spec/a~0b",
		doc:     `{"spec":{"a~b":1,"keep":2}}`,
		want:    `{"spec":{"keep":2}}`,
	},
	{
		// json-patch rewrites only "~0" and "~1" and leaves any other "~" sequence
		// literal, so this addresses a key really named "a~2b". A stricter RFC 6901
		// check would reject it and break configurations which work today.
		pointer: "/spec/a~2b",
		doc:     `{"spec":{"a~2b":1,"keep":2}}`,
		want:    `{"spec":{"keep":2}}`,
	},
	{
		pointer: "/spec/trailing~",
		doc:     `{"spec":{"trailing~":1,"keep":2}}`,
		want:    `{"spec":{"keep":2}}`,
	},
}

// rejectedJSONPointers are values which must never be accepted, each with a
// document and the result the user was aiming for. "intended" is what the pointer
// would have produced had it addressed what the user wrote; the assertion is that
// json-patch does not produce it — it either fails outright, or, for a value with
// two segments and no leading slash, quietly hits something else.
var rejectedJSONPointers = []struct {
	pointer  string
	doc      string
	intended string
}{
	{
		// RFC 6901 reads "" as the whole document; Fleet rejects it because it
		// reaches json-patch as a missing path instead.
		pointer:  "",
		doc:      `{"spec":{"replicas":3}}`,
		intended: `{}`,
	},
	{
		// The common mistake: a Kubernetes-style field path.
		pointer:  "spec.replicas",
		doc:      `{"spec":{"replicas":3}}`,
		intended: `{"spec":{}}`,
	},
	{
		pointer:  "spec",
		doc:      `{"spec":{},"status":{}}`,
		intended: `{"status":{}}`,
	},
	{
		// The dangerous one. json-patch splits on "/" and walks
		// split[1:len(split)-1], which is empty here, so this addresses "kind" at
		// the root of the document and really does rewrite the resource's kind
		// rather than the metadata.kind the user aimed at.
		pointer:  "metadata/kind",
		doc:      `{"kind":"Service","metadata":{"kind":"Pod"}}`,
		intended: `{"kind":"Service","metadata":{}}`,
	},
}

// TestNormalizers_AgreesWithJSONPointerValidation pins validation.IsJSONPointer,
// which fleet apply rejects a comparePatch path and a jsonPointers entry with, to
// what the agent can really address. Both consumers are driven: the fleet.Operation
// marshalling newNormalizers does for an operation path, and the
// map[string]string{"op": "remove", "path": …} the ignore normalizer builds for a
// jsonPointers entry (internal/normalizers/diff_normalizer.go:51). A pointer the
// predicate accepts has to resolve to the key the user wrote in both; one it
// rejects must not.
func TestNormalizers_AgreesWithJSONPointerValidation(t *testing.T) {
	for _, tc := range acceptedJSONPointers {
		t.Run(fmt.Sprintf("accepted pointer %q", tc.pointer), func(t *testing.T) {
			if !validation.IsJSONPointer(tc.pointer) {
				t.Fatalf("IsJSONPointer(%q) = false, but the agent can address it", tc.pointer)
			}

			for shape, remove := range removeByPointer {
				got, err := remove(t, tc.pointer, tc.doc)
				if err != nil {
					t.Errorf(
						"IsJSONPointer(%q) = true, but the agent cannot address it as %s: %v",
						tc.pointer, shape, err,
					)
					continue
				}

				if !jsonpatch.Equal(got, []byte(tc.want)) {
					t.Errorf(
						"removing %q as %s from %s gave %s, want %s: the pointer does not address what the user wrote",
						tc.pointer, shape, tc.doc, got, tc.want,
					)
				}
			}
		})
	}

	for _, tc := range rejectedJSONPointers {
		t.Run(fmt.Sprintf("rejected pointer %q", tc.pointer), func(t *testing.T) {
			if validation.IsJSONPointer(tc.pointer) {
				t.Errorf("IsJSONPointer(%q) = true, but the agent cannot address it", tc.pointer)
			}

			for shape, remove := range removeByPointer {
				got, err := remove(t, tc.pointer, tc.doc)
				if err != nil {
					continue // failing outright is the expected outcome
				}

				if jsonpatch.Equal(got, []byte(tc.intended)) {
					t.Errorf(
						"removing %q as %s from %s gave %s, which is what the user meant; it is rejected by the validator, so the two disagree",
						tc.pointer, shape, tc.doc, got,
					)
				}
			}
		})
	}
}

// removeByPointer holds the two ways a comparePatch pointer reaches json-patch,
// so every case is driven through both.
var removeByPointer = map[string]func(t *testing.T, pointer, doc string) ([]byte, error){
	"an operation path":    removeViaOperation,
	"a jsonPointers entry": removeViaIgnoreNormalizer,
}

// removeViaOperation marshals a real fleet.Operation exactly as newNormalizers
// does for a comparePatch operation, then applies it the way JSONPatchNormalizer
// does.
func removeViaOperation(t *testing.T, pointer, doc string) ([]byte, error) {
	t.Helper()

	patchData, err := json.Marshal([]any{fleet.Operation{Op: "remove", Path: pointer}})
	if err != nil {
		t.Fatalf("marshalling an operation for pointer %q: %v", pointer, err)
	}

	return applyRemove(patchData, doc)
}

// removeViaIgnoreNormalizer builds the patch the vendored ignore normalizer builds
// for a jsonPointers entry at
// internal/cmd/agent/deployer/internal/normalizers/diff_normalizer.go:51. The shape
// differs from an operation's: map[string]string has no omitempty, so an empty
// pointer arrives as an empty path rather than as a missing one.
func removeViaIgnoreNormalizer(t *testing.T, pointer, doc string) ([]byte, error) {
	t.Helper()

	patchData, err := json.Marshal([]map[string]string{{"op": "remove", "path": pointer}})
	if err != nil {
		t.Fatalf("marshalling an ignore patch for pointer %q: %v", pointer, err)
	}

	return applyRemove(patchData, doc)
}

func applyRemove(patchData []byte, doc string) ([]byte, error) {
	patch, err := jsonpatch.DecodePatch(patchData)
	if err != nil {
		return nil, err
	}

	return patch.Apply([]byte(doc))
}
