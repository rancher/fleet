package validation

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// PreservePathToken is one reference token of a helm.preserveOnReplace path.
type PreservePathToken struct {
	// Field is the unescaped object key the token addresses. It is empty if
	// ListKey is set.
	Field string
	// ListKey is set for a "[key]" token, which iterates over the items of a
	// list, pairing items by the value of their field ListKey.
	ListKey string
}

// neverPreservedPaths cannot be kept from the live object by
// helm.preserveOnReplace, nor anything below them: they identify the object,
// are managed by the API server, or are not part of what a PUT changes.
var neverPreservedPaths = [][]string{
	{"status"},
	{"apiVersion"},
	{"kind"},
	{"metadata", "name"},
	{"metadata", "namespace"},
	{"metadata", "uid"},
	{"metadata", "resourceVersion"},
	{"metadata", "generation"},
	{"metadata", "creationTimestamp"},
	{"metadata", "deletionTimestamp"},
	{"metadata", "managedFields"},
}

// ParsePreservePath parses a helm.preserveOnReplace path: an RFC 6901 JSON
// pointer, in which a "[key]" token iterates over the items of a list.
//
// Unlike comparePatches pointers, which reach json-patch and have to stay as lax
// as json-patch is, this is strict RFC 6901: "~" must be followed by "0" or "1".
// Empty tokens are rejected, a list token can be neither the first nor the last
// one and must be followed by a field, not by another list token, and paths at
// or below neverPreservedPaths are rejected. A field whose name starts with "["
// and ends with "]" cannot be addressed, as its token is read as a list token.
func ParsePreservePath(path string) ([]PreservePathToken, error) {
	if !strings.HasPrefix(path, "/") {
		return nil, InvalidJSONPointerError(path)
	}

	raw := strings.Split(path[1:], "/")
	tokens := make([]PreservePathToken, 0, len(raw))
	fields := make([]string, 0, len(raw))
	for i, r := range raw {
		if r == "" {
			return nil, fmt.Errorf("invalid path %q: token %d is empty", path, i)
		}
		if isListToken(r) {
			key, err := unescapeToken(path, r[1:len(r)-1])
			if err != nil {
				return nil, err
			}
			if key == "" {
				return nil, fmt.Errorf("invalid path %q: list token %q must name a key, e.g. \"[name]\"", path, r)
			}
			if i == 0 || i == len(raw)-1 {
				return nil, fmt.Errorf("invalid path %q: list token %q must be followed by a field and cannot be the first token", path, r)
			}
			if isListToken(raw[i+1]) {
				return nil, fmt.Errorf("invalid path %q: list token %q must be followed by a field, not by list token %q: lists of lists are not supported", path, r, raw[i+1])
			}
			tokens = append(tokens, PreservePathToken{ListKey: key})
			continue
		}
		field, err := unescapeToken(path, r)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, PreservePathToken{Field: field})
		fields = append(fields, field)
	}

	// Only a prefix made of plain fields can reach neverPreservedPaths.
	plain := len(fields)
	for i, t := range tokens {
		if t.ListKey != "" {
			plain = i
			break
		}
	}
	for _, never := range neverPreservedPaths {
		if plain >= len(never) && slices.Equal(fields[:len(never)], never) {
			return nil, fmt.Errorf("invalid path %q: /%s cannot be preserved", path, strings.Join(never, "/"))
		}
	}

	return tokens, nil
}

// isListToken reports whether the raw (escaped) reference token r is a "[key]"
// list token.
func isListToken(r string) bool {
	return strings.HasPrefix(r, "[") && strings.HasSuffix(r, "]")
}

// unescapeToken unescapes a JSON pointer reference token, as in RFC 6901.
func unescapeToken(path, token string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(token); i++ {
		if token[i] != '~' {
			b.WriteByte(token[i])
			continue
		}
		i++
		switch {
		case i < len(token) && token[i] == '0':
			b.WriteByte('~')
		case i < len(token) && token[i] == '1':
			b.WriteByte('/')
		default:
			return "", fmt.Errorf(`invalid path %q: "~" must be followed by "0" or "1"`, path)
		}
	}
	return b.String(), nil
}

// ValidatePreserveOnReplace checks every helm.preserveOnReplace entry in opts
// (see CheckPreserveOnReplace). An entry which fails would be skipped by the
// agent, which only logs it, so the fields the user meant to keep would be
// cleared by the next forced replace.
func ValidatePreserveOnReplace(path string, opts *fleet.BundleDeploymentOptions) error {
	if opts.Helm == nil {
		return nil
	}

	for i, rule := range opts.Helm.PreserveOnReplace {
		if err := CheckPreserveOnReplace(rule); err != nil {
			return fmt.Errorf("%shelm.preserveOnReplace[%d].%w", path, i, err)
		}
	}

	return nil
}

// CheckPreserveOnReplace reports why a helm.preserveOnReplace entry cannot be
// used, or nil if it can. The error names the offending field relative to the
// entry, e.g. "paths[1].path: ...". The agent uses it to skip entries which
// reach it without having been validated. The nameRegex is only checked when
// no name is set, as the name takes precedence over it.
func CheckPreserveOnReplace(rule fleet.PreserveOnReplaceRule) error {
	if rule.APIVersion == "" {
		return errors.New("apiVersion: must not be empty")
	}
	if rule.Kind == "" {
		return errors.New("kind: must not be empty")
	}
	if rule.Name == "" && rule.NameRegex != "" {
		if _, err := regexp.Compile(rule.NameRegex); err != nil {
			return fmt.Errorf("nameRegex: invalid regular expression %q: %w", rule.NameRegex, err)
		}
	}
	if len(rule.Paths) == 0 {
		return errors.New("paths: must not be empty")
	}
	for j, p := range rule.Paths {
		if _, err := ParsePreservePath(p.Path); err != nil {
			return fmt.Errorf("paths[%d].path: %w", j, err)
		}
		if p.MergeKey != strings.TrimSpace(p.MergeKey) {
			return fmt.Errorf("paths[%d].mergeKey: invalid key %q: must not start or end with white space", j, p.MergeKey)
		}
	}

	return nil
}
