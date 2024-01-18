package git

import (
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
)

// branchInvalidContains defines sequence os characters that are not
// allow within a branch name.
//
// Rules as per: https://git-scm.com/docs/git-check-ref-format
var branchInvalidContains = []string{
	"..",
	"//",
	"?",
	"*",
	"[",
	"@{",
	"\\",
	" ",
	"~",
	"^",
	":",
}

const (
	// branch naming rules as per:
	// - https://git-scm.com/docs/git-check-ref-format
	branchMaxLength     = 255
	branchInvalidSuffix = ".lock"
	branchInvalidPrefix = "."
	branchInvalidValues = "@"

	// urlMaxLength represents the max length accepted for git URLs.
	//
	// Value is based on libgit2's:
	// - https://github.com/libgit2/libgit2/blob/936b184e7494158c20e522981f4a324cac6ffa47/src/util/win32/w32_common.h#L13-L18
	// - https://github.com/libgit2/libgit2/blob/936b184e7494158c20e522981f4a324cac6ffa47/include/git2/common.h#L103-L106
	urlMaxLength = 4096
)

// validateBranch validates a branch name and return an error in case it is
// invalid.
//
// Implementation can be cross-checked with upstream:
// - https://github.com/git/git/blob/4dbebc36b0893f5094668ddea077d0e235560b16/refs.c
func validateBranch(name string) error {
	switch {
	case len(name) > branchMaxLength:
		return fmt.Errorf("invalid branch name: too long")
	case strings.HasSuffix(name, branchInvalidSuffix):
		return fmt.Errorf("invalid branch name: cannot end with %q", branchInvalidSuffix)
	case strings.HasPrefix(name, branchInvalidPrefix):
		return fmt.Errorf("invalid branch name: cannot start with %q", branchInvalidPrefix)
	case name == branchInvalidValues:
		return fmt.Errorf("invalid branch name: %q", name)
	}

	for _, invalid := range branchInvalidContains {
		if strings.Contains(name, invalid) {
			return fmt.Errorf("invalid branch name: cannot contain %q", invalid)
		}
	}

	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("invalid branch name: control chars are not supported")
		}
	}

	return nil
}

// validateCommit validates a commit and returns an error in case it is invalid.
func validateCommit(commit string) error {
	switch len(commit) {
	// git supports SHA1 and SHA256 (experimental).
	case 40, 64:
		if _, err := hex.DecodeString(commit); err != nil {
			return fmt.Errorf("invalid commit ID: %q is not a valid hex", commit)
		}
		return nil
	default:
		return fmt.Errorf("invalid commit ID: %q", commit)
	}
}

func validateURL(u string) error {
	switch {
	case u == "":
		return fmt.Errorf("invalid url: cannot be empty")
	case len(u) > urlMaxLength:
		return fmt.Errorf("invalid url: exceeds max length %d", urlMaxLength)
	default:
		return nil
	}
}
