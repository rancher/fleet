// Package helmversion deals with the two spellings a Helm chart version can
// have, and orders versions in a way which takes build metadata into account.
//
// OCI registries do not allow a plus sign in a tag, so Helm publishes a chart
// whose version carries build metadata under a tag where the plus sign is
// replaced with an underscore: version 109.0.1+up1.0.2 lives in the tag
// 109.0.1_up1.0.2.
//
// The semver specification also mandates that build metadata be ignored when
// comparing versions, which is unhelpful when the metadata is the only thing
// telling two versions apart. Comparison here therefore falls back to the
// metadata for versions semver considers equal, so that resolving a version
// against a set of tags always yields the same answer, whatever order the
// registry lists them in.
package helmversion

import (
	"cmp"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
)

const (
	// tagMetadataSeparator is what an OCI tag uses in place of the semver build
	// metadata separator.
	tagMetadataSeparator = "_"
	metadataSeparator    = "+"

	// vPrefix is what a Helm repository publishing versions the way a git tag
	// does puts in front of them.
	vPrefix = "v"
)

// FromTag returns the version an OCI tag holds, restoring the build metadata
// separator Helm replaced to make the tag acceptable to a registry.
func FromTag(tag string) string {
	return strings.ReplaceAll(tag, tagMetadataSeparator, metadataSeparator)
}

// Parse parses a chart version, rejecting a version semver only accepts loosely,
// such as 1.2 for 1.2.0. Fleet hands the version it resolves on as the chart
// version, and a loose spelling is refused further down, before a single bundle
// deployment is created, so a tag holding one is no candidate at all.
//
// A leading "v" is tolerated, as some Helm repositories publish their versions
// that way and Fleet accepts them
func Parse(v string) (*semver.Version, error) {
	if _, err := semver.StrictNewVersion(strings.TrimPrefix(v, vPrefix)); err != nil {
		return nil, err
	}

	return semver.NewVersion(v)
}

// Normalize returns v spelled as semver, accepting the tag spelling of build
// metadata users may copy from a registry's list of tags. Anything which is not
// a version, a version constraint in particular, is returned unchanged.
func Normalize(v string) string {
	if _, err := semver.NewVersion(v); err == nil {
		return v
	}

	if converted := FromTag(v); converted != v {
		if _, err := semver.NewVersion(converted); err == nil {
			return converted
		}
	}

	return v
}

// ParseExact parses v as a fully specified version, in either spelling of build
// metadata. It returns false for anything else, such as a version constraint, a
// partial version or an empty string.
//
// A version parsed this way still leaves build metadata open unless it spells
// some out: 109.0.1 denotes every build of 109.0.1, whereas 109.0.1+up1.0.2
// denotes that one build alone.
func ParseExact(v string) (*semver.Version, bool) {
	sv, err := semver.StrictNewVersion(Normalize(v))
	if err != nil {
		return nil, false
	}

	return sv, true
}

// IsExact reports whether v is a fully specified version, as opposed to a
// constraint which versions other than v may satisfy.
func IsExact(v string) bool {
	_, ok := ParseExact(v)

	return ok
}

// Equal reports whether a and b are the same version, build metadata included.
// This is stricter than [semver.Version.Equal], which ignores metadata and
// would therefore consider 1.1.2 and 1.1.2+build01 to be the same version.
func Equal(a, b *semver.Version) bool {
	return a.Equal(b) && a.Metadata() == b.Metadata()
}

// Compare orders two versions by precedence as semver defines it, and then, for
// versions semver considers equal, by build metadata. A version with metadata
// takes precedence over the same version without it, so that version 109.0.1
// resolves to the build published as 109.0.1+up1.0.2, which is the chart a
// registry following the convention actually holds.
func Compare(a, b *semver.Version) int {
	if c := a.Compare(b); c != 0 {
		return c
	}

	am, bm := a.Metadata(), b.Metadata()
	switch {
	case am == bm:
		return 0
	case am == "":
		return -1
	case bm == "":
		return 1
	}

	return CompareMetadata(am, bm)
}

// CompareMetadata orders two build metadata strings. The semver specification
// says nothing about their precedence, so they are compared the way it defines
// for pre-release strings: identifier by identifier, numerically wherever both
// identifiers are numeric. That keeps up1.0.10 above up1.0.2, which comparing
// the strings as a whole would not. Digits which are part of a longer
// identifier are not numbers of their own, again as for pre-release
// identifiers, so up10 does compare below up2.
func CompareMetadata(a, b string) int {
	aIDs, bIDs := strings.Split(a, "."), strings.Split(b, ".")

	for i := range min(len(aIDs), len(bIDs)) {
		if c := compareIdentifier(aIDs[i], bIDs[i]); c != 0 {
			return c
		}
	}

	// Every identifier they have in common is equal, so the string with more of
	// them takes precedence.
	return cmp.Compare(len(aIDs), len(bIDs))
}

// compareIdentifier orders two dot separated identifiers, following the
// precedence rules the semver specification gives for pre-release identifiers:
// numeric identifiers compare numerically, and rank below alphanumeric ones.
func compareIdentifier(a, b string) int {
	aNum, aErr := strconv.ParseUint(a, 10, 64)
	bNum, bErr := strconv.ParseUint(b, 10, 64)

	switch {
	case aErr == nil && bErr == nil:
		return cmp.Compare(aNum, bNum)
	case aErr == nil:
		return -1
	case bErr == nil:
		return 1
	}

	return strings.Compare(a, b)
}
