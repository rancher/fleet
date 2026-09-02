// Package validation holds the predicates which the fleet.yaml validation run by
// `fleet apply` and the agent code consuming those values have to agree on. A
// rule which lives in one place cannot drift apart between the two.
//
// It must stay a leaf package: a rule kept here has no fleet-internal
// dependencies beyond pkg/apis, so both the validator and the agent can import
// it freely, whatever else either of them already pulls in.
package validation

import (
	"fmt"
	"slices"
	"strings"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// supportedPatchOps lists the operations a diff.comparePatches operation may
// use: Fleet's own "ignore", which the agent handles itself, plus the RFC 6902
// operations which github.com/evanphx/json-patch can apply to what Fleet is able
// to give it.
//
// "copy" and "move" are deliberately absent even though json-patch implements
// both: fleet.Operation has no "from" field, so a copy or a move can never be
// encoded in a form json-patch accepts. It always fails with "missing from
// field", which makes both of them dead config by any route.
//
// Kept sorted, because SupportedPatchOps feeds error messages.
var supportedPatchOps = []string{
	"add",
	fleet.IgnoreOp,
	"remove",
	"replace",
	"test",
}

// IsSupportedPatchOp reports whether op is an operation Fleet can apply as part
// of a diff comparePatch. The empty string is not one: an operation without an
// op is not a no-op, it makes the patch fail.
func IsSupportedPatchOp(op string) bool {
	return slices.Contains(supportedPatchOps, op)
}

// SupportedPatchOps returns the operations accepted by IsSupportedPatchOp,
// sorted, for use in error messages.
func SupportedPatchOps() []string {
	return slices.Clone(supportedPatchOps)
}

// UnsupportedPatchOpError returns the error reported for a diff comparePatch
// operation IsSupportedPatchOp rejects. It lives here so that the validator and
// the agent, which both have to explain the same rejection, cannot word it
// differently or list different operations.
func UnsupportedPatchOpError(op string) error {
	return fmt.Errorf("unsupported operation %q, valid values are: %s", op, strings.Join(supportedPatchOps, ", "))
}
