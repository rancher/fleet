package validation

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// ForEachBundleSpecOptions calls fn for every BundleDeploymentOptions reachable
// from a BundleSpec — the spec's own options and those of each target — with the
// field path prefix which identifies it to the user, below the given prefix.
//
// BundleSpec is what fleet.yaml and a HelmOp have in common, so a rule walked
// with this function is checked identically on both. Note that
// TargetCustomizations are *not* part of BundleSpec: a fleet.yaml has that third
// site and has to walk it itself (see bundlereader.forEachOptions).
func ForEachBundleSpecOptions(
	prefix string,
	spec *fleet.BundleSpec,
	fn func(path string, opts *fleet.BundleDeploymentOptions) error,
) error {
	if err := fn(prefix, &spec.BundleDeploymentOptions); err != nil {
		return err
	}

	for i := range spec.Targets {
		if err := fn(fmt.Sprintf("%stargets[%d].", prefix, i), &spec.Targets[i].BundleDeploymentOptions); err != nil {
			return err
		}
	}

	return nil
}

// BundleDeploymentOptionChecks are the rules which apply to a
// BundleDeploymentOptions whichever entry point it was written at. Both
// bundlereader.validateFleetYAML and the HelmOp reconciler's validateBundleSpec
// range over this one list, so a rule added here cannot end up enforced on
// fleet.yaml but not on a HelmOp, or the other way round.
//
// Each entry takes the field path prefix identifying the options to the user, so
// the caller decides how paths are reported (see ForEachBundleSpecOptions).
var BundleDeploymentOptionChecks = []func(path string, opts *fleet.BundleDeploymentOptions) error{
	ValidateComparePatchNames,
	ValidateComparePatchOperations,
	ValidateComparePatchOperationPaths,
	ValidateComparePatchJSONPointers,
}

// ValidateComparePatchNames checks that every diff.comparePatches[].name in opts compiles
// as a regular expression. The agent matches a patch by exact name first and falls
// back to matching the name as a regex, so a name which does not compile is dead
// config: it is dropped by the agent instead of being reported to the user.
func ValidateComparePatchNames(path string, opts *fleet.BundleDeploymentOptions) error {
	if opts.Diff == nil {
		return nil
	}

	for i, patch := range opts.Diff.ComparePatches {
		// An empty name means "match every name" (see desiredset.diff).
		if patch.Name == "" {
			continue
		}

		if _, err := regexp.Compile(patch.Name); err != nil {
			return fmt.Errorf(
				"%sdiff.comparePatches[%d].name: invalid regular expression %q: %w",
				path, i, patch.Name, err,
			)
		}
	}

	return nil
}

// ValidateComparePatchOperations checks that every diff.comparePatches[].operations[].op
// in opts names an operation Fleet can apply. Anything else is dead config: the
// agent marshals the operation into a JSON patch which json-patch then refuses to
// apply, and that failure is only logged, so the patch does nothing. An empty op
// is rejected too; it fails in exactly the same way rather than being the no-op it
// looks like.
func ValidateComparePatchOperations(path string, opts *fleet.BundleDeploymentOptions) error {
	if opts.Diff == nil {
		return nil
	}

	for i, patch := range opts.Diff.ComparePatches {
		for j, op := range patch.Operations {
			if IsSupportedPatchOp(op.Op) {
				continue
			}

			return fmt.Errorf(
				"%sdiff.comparePatches[%d].operations[%d].op: %w",
				path, i, j, UnsupportedPatchOpError(op.Op),
			)
		}
	}

	return nil
}

// ValidateComparePatchOperationPaths checks that every diff.comparePatches[].operations[].path
// in opts is a JSON pointer the agent can address. Anything else is dead config, in one
// of two ways: usually the agent marshals the operation into a JSON patch whose path
// json-patch fails to resolve, and because that failure only reaches a log line the
// patch silently does nothing — taking the other operations held for the same resource
// with it. Occasionally it is worse than that, because json-patch resolves the path to
// something the user did not write: see IsJSONPointer for the two-segment case.
//
// An "ignore" operation is skipped: it removes the whole resource from the diff and
// its path is never read (see desiredset.Diff).
func ValidateComparePatchOperationPaths(path string, opts *fleet.BundleDeploymentOptions) error {
	if opts.Diff == nil {
		return nil
	}

	for i, patch := range opts.Diff.ComparePatches {
		for j, op := range patch.Operations {
			if op.Op == fleet.IgnoreOp {
				continue
			}

			if err := checkJSONPointer(op.Path); err != nil {
				return fmt.Errorf(
					"%sdiff.comparePatches[%d].operations[%d].path: %w",
					path, i, j, err,
				)
			}
		}
	}

	return nil
}

// ValidateComparePatchJSONPointers checks that every entry of every
// diff.comparePatches[].jsonPointers in opts is a JSON pointer the agent can
// address. A malformed entry is dead config, and the most invisible kind Fleet
// has: the ignore normalizer logs the failure at V(1) info level, which is
// filtered out at the agent's default verbosity, so nothing is reported at all
// and the field the user asked to ignore keeps showing up as drift. An entry
// which json-patch does resolve, but not to what the user wrote, reports nothing
// either and ignores the wrong field: see IsJSONPointer for the two-segment case.
func ValidateComparePatchJSONPointers(path string, opts *fleet.BundleDeploymentOptions) error {
	if opts.Diff == nil {
		return nil
	}

	for i, patch := range opts.Diff.ComparePatches {
		for j, pointer := range patch.JsonPointers {
			if err := checkJSONPointer(pointer); err != nil {
				return fmt.Errorf(
					"%sdiff.comparePatches[%d].jsonPointers[%d]: %w",
					path, i, j, err,
				)
			}
		}
	}

	return nil
}

// IsJSONPointer reports whether value can be used as a JSON pointer by a diff
// comparePatch — as an operation path or as a jsonPointers entry, which end up in
// the same json-patch call. A pointer has to be non-empty and start with "/".
//
// The rule is deliberately only what json-patch cannot resolve to what the user
// wrote. It is not a full RFC 6901 check, because json-patch is laxer than the RFC
// in a way real configurations depend on — it rewrites "~0" and "~1" and leaves any
// other "~" sequence alone, so "/spec/a~2b" legitimately addresses a key named
// "a~2b" and must keep working.
//
// The missing leading slash is not merely a pointer which fails to resolve. Whether
// it fails depends on how many segments it has, because json-patch splits the path
// on "/" and then walks split[1:len(split)-1]: "spec.replicas" has one segment and
// addresses nothing, but "metadata/kind" has two, leaves nothing to walk, and so
// addresses the key "kind" at the *root* of the document — a patch on it really does
// rewrite the resource's kind, silently and nowhere near where the user aimed it.
//
// The empty pointer is rejected even though RFC 6901 gives it a meaning (the whole
// document): Operation.Path is marshalled with omitempty, so an empty path reaches
// json-patch as a missing "path" field and always fails with "operation missing
// path field".
func IsJSONPointer(value string) bool {
	return strings.HasPrefix(value, "/")
}

// InvalidJSONPointerError returns the error reported for a diff comparePatch
// pointer IsJSONPointer rejects. It lives here so that the validator and the
// agent, which both have to explain the same rejection, cannot word it
// differently.
func InvalidJSONPointerError(value string) error {
	if value == "" {
		return errors.New(`invalid JSON pointer "": must not be empty, e.g. "/spec/replicas"`)
	}

	// A Kubernetes-style field path is the common mistake, so the hint spells out
	// the pointer the user probably meant — where that can be done without
	// inventing one, and the fixed hint otherwise.
	hint := "/spec/replicas"
	if converted, ok := dottedFieldPathHint(value); ok {
		hint = converted
	}

	return fmt.Errorf("invalid JSON pointer %q: must start with %q, e.g. %q", value, "/", hint)
}

// dottedFieldPathHint converts a Kubernetes-style field path such as
// "spec.replicas" into the JSON pointer it was probably meant to be, reporting
// false where converting the dots would produce something wrong or misleading.
//
// A wrong hint is worse here than no hint: this message is written to be copied,
// and a value copied out of it that Fleet then accepts but the agent cannot use
// recreates the silent failure the check exists to prevent.
func dottedFieldPathHint(value string) (string, bool) {
	// A "/" or a "~" means at least one dot belongs to a key rather than being a
	// separator: "metadata.annotations.deployment.kubernetes.io/revision" is a
	// single annotation, whose pointer is
	// "/metadata/annotations/deployment.kubernetes.io~1revision", not one segment
	// per dot.
	if strings.ContainsAny(value, "/~") {
		return "", false
	}

	// A leading, trailing or doubled dot converts to an empty segment, which is
	// how a hint ends up worse than useless: ".spec.replicas" would suggest
	// "//spec/replicas", and that *passes* IsJSONPointer, so a user who copies it
	// gets a value fleet apply accepts and the agent then fails on silently.
	if strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") || strings.Contains(value, "..") {
		return "", false
	}

	return "/" + strings.ReplaceAll(value, ".", "/"), true
}

// checkJSONPointer reports why value cannot be used as a JSON pointer, or nil if
// it can.
func checkJSONPointer(value string) error {
	if IsJSONPointer(value) {
		return nil
	}

	return InvalidJSONPointerError(value)
}
