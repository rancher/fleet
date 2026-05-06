package git

import (
	"encoding/hex"
	"errors"
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
)

// validateBranch validates a branch name and return an error in case it is
// invalid.
//
// Implementation can be cross-checked with upstream:
// - https://github.com/git/git/blob/4dbebc36b0893f5094668ddea077d0e235560b16/refs.c
func validateBranch(name string) error {
	switch {
	case len(name) > branchMaxLength:
		return errors.New("invalid branch name: too long")
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
			return errors.New("invalid branch name: control chars are not supported")
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
