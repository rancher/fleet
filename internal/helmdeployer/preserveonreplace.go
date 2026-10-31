package helmdeployer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"

	"github.com/go-logr/logr"

	"github.com/rancher/fleet/internal/validation"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// preserveRule is a compiled helm.preserveOnReplace entry.
type preserveRule struct {
	apiVersion string
	kind       string
	namespace  string
	name       string
	nameRegexp *regexp.Regexp
	paths      []preservePath
}

// preservePath is a parsed helm.preserveOnReplace path.
type preservePath struct {
	path     string
	tokens   []validation.PreservePathToken
	mergeKey string
}

// compilePreserveRules compiles the helm.preserveOnReplace entries of a bundle
// deployment. Entries which cannot be used are skipped and reported in the
// returned error, the others are still returned: fleet apply and the HelmOp
// controller reject them, but bundle deployments can be created by other means.
func compilePreserveRules(entries []fleet.PreserveOnReplaceRule) ([]preserveRule, error) {
	var (
		rules []preserveRule
		errs  []error
	)
	for i, e := range entries {
		if err := validation.CheckPreserveOnReplace(e); err != nil {
			errs = append(errs, fmt.Errorf("helm.preserveOnReplace[%d].%w", i, err))
			continue
		}
		r := preserveRule{apiVersion: e.APIVersion, kind: e.Kind, namespace: e.Namespace, name: e.Name}
		if e.Name == "" && e.NameRegex != "" {
			// Cannot fail, CheckPreserveOnReplace compiled it.
			r.nameRegexp = regexp.MustCompile(e.NameRegex)
		}
		for _, p := range e.Paths {
			tokens, _ := validation.ParsePreservePath(p.Path)
			r.paths = append(r.paths, preservePath{path: p.Path, tokens: tokens, mergeKey: p.MergeKey})
		}
		rules = append(rules, r)
	}

	return rules, errors.Join(errs...)
}

// preserveRulesFor compiles the helm.preserveOnReplace entries of a bundle
// deployment for getCfg. Invalid entries are skipped and logged: as an error
// if report is set, else at V(1). Callers set report for the install which
// applies a deployment, so that the error is logged once per deployment rather
// than also for its dry run and for every drift correction.
func preserveRulesFor(ctx context.Context, entries []fleet.PreserveOnReplaceRule, report bool) []preserveRule {
	if len(entries) == 0 {
		return nil
	}
	rules, err := compilePreserveRules(entries)
	if err != nil {
		logger := log.FromContext(ctx).WithName("preserve-on-replace")
		if report {
			logger.Error(err, "Ignoring invalid helm.preserveOnReplace entries")
		} else {
			logger.V(1).Info("Ignoring invalid helm.preserveOnReplace entries", "error", err.Error())
		}
	}
	return rules
}

// matches reports whether the rule applies to an object: apiVersion and kind
// must equal the rendered ones, the namespace must too unless the rule has
// none, and the name must equal the rendered one if the rule has a name, or
// else match the rule's unanchored nameRegex if it has one. This differs from
// each of the ways comparePatches match objects.
func (r preserveRule) matches(apiVersion, kind, namespace, name string) bool {
	if r.apiVersion != apiVersion || r.kind != kind {
		return false
	}
	if r.namespace != "" && r.namespace != namespace {
		return false
	}
	switch {
	case r.name != "":
		return r.name == name
	case r.nameRegexp != nil:
		return r.nameRegexp.MatchString(name)
	default:
		return true
	}
}

// preservedPaths returns the paths of all rules matching an object.
func preservedPaths(rules []preserveRule, apiVersion, kind, namespace, name string) []preservePath {
	var paths []preservePath
	for _, r := range rules {
		if r.matches(apiVersion, kind, namespace, name) {
			paths = append(paths, r.paths...)
		}
	}
	return paths
}

// preserveLiveFields copies the values at paths from live to target, following
// the rules of helm.preserveOnReplace. It reports whether target changed. Paths
// which cannot be followed, e.g. through a string or through a list without a
// "[key]" token, are skipped and reported in the returned error, while the
// other paths are still copied. Live items a mergeKey cannot merge are logged
// at V(1) with logger.
func preserveLiveFields(logger logr.Logger, target, live map[string]any, paths []preservePath) (bool, error) {
	var (
		changed bool
		errs    []error
	)
	for _, p := range paths {
		c, err := preserveAt(logger.WithValues("path", p.path), target, live, p.tokens, p.mergeKey)
		if err != nil {
			errs = append(errs, fmt.Errorf("path %q: %w", p.path, err))
		}
		changed = changed || c
	}

	return changed, errors.Join(errs...)
}

// preserveAt copies the value at tokens, which must start with a field token,
// from live to target.
func preserveAt(logger logr.Logger, target, live map[string]any, tokens []validation.PreservePathToken, mergeKey string) (bool, error) {
	if len(tokens) == 0 || tokens[0].ListKey != "" {
		// validation.ParsePreservePath rejects such paths.
		return false, errors.New("a list token must follow a field")
	}
	field := tokens[0].Field
	liveValue, ok := live[field]
	if !ok || liveValue == nil {
		return false, nil // nothing to copy
	}

	if len(tokens) == 1 {
		if mergeKey != "" {
			return mergeList(logger, target, field, liveValue, mergeKey)
		}
		if v, ok := target[field]; ok && v != nil {
			return false, nil // set in the manifest, which wins
		}
		target[field] = runtime.DeepCopyJSONValue(liveValue)
		return true, nil
	}

	rest := tokens[1:]
	if rest[0].ListKey != "" {
		return preserveInPairedItems(logger, target, field, liveValue, rest[0].ListKey, rest[1:], mergeKey)
	}

	liveChild, ok := liveValue.(map[string]any)
	if !ok {
		return false, fmt.Errorf("%q is not an object on the live object", field)
	}
	var targetChild map[string]any
	switch v := target[field].(type) {
	case map[string]any:
		targetChild = v
	case nil:
		// Only attached to target if something is copied into it, so that no
		// empty object is added.
		targetChild = map[string]any{}
	default:
		return false, fmt.Errorf("%q is not an object in the manifest", field)
	}

	changed, err := preserveAt(logger, targetChild, liveChild, rest, mergeKey)
	if changed && target[field] == nil {
		target[field] = targetChild
	}
	return changed, err
}

// preserveInPairedItems follows tokens into each item of the list at field in
// target, paired with the item of the live list which has the same value of
// listKey. Items without a pair are skipped.
func preserveInPairedItems(logger logr.Logger, target map[string]any, field string, liveValue any, listKey string, tokens []validation.PreservePathToken, mergeKey string) (bool, error) {
	liveItems, ok := liveValue.([]any)
	if !ok {
		return false, fmt.Errorf("%q is not a list on the live object", field)
	}
	var targetItems []any
	switch v := target[field].(type) {
	case []any:
		targetItems = v
	case nil:
		return false, nil // no items to pair
	default:
		return false, fmt.Errorf("%q is not a list in the manifest", field)
	}

	var (
		changed bool
		errs    []error
	)
	for _, t := range targetItems {
		targetItem, ok := t.(map[string]any)
		if !ok {
			continue
		}
		key, ok := targetItem[listKey]
		if !ok || key == nil {
			continue
		}
		liveItem := findItem(liveItems, listKey, key)
		if liveItem == nil {
			continue
		}
		c, err := preserveAt(logger, targetItem, liveItem, tokens, mergeKey)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s[%s=%v]: %w", field, listKey, key, err))
		}
		changed = changed || c
	}

	return changed, errors.Join(errs...)
}

// findItem returns the first object in items whose field key equals value.
func findItem(items []any, key string, value any) map[string]any {
	for _, i := range items {
		if item, ok := i.(map[string]any); ok && reflect.DeepEqual(item[key], value) {
			return item
		}
	}
	return nil
}

// mergeList appends to the list at field in target the items of liveValue
// whose mergeKey value no item of the target list has, in live order. Live
// items without mergeKey are skipped, and so are live items which are not
// objects, which are logged: mergeKey only merges lists of objects. If target
// has no list, the live list is copied, whatever its items.
func mergeList(logger logr.Logger, target map[string]any, field string, liveValue any, mergeKey string) (bool, error) {
	liveItems, ok := liveValue.([]any)
	if !ok {
		return false, fmt.Errorf("%q is not a list on the live object, mergeKey needs one", field)
	}

	var targetItems []any
	switch v := target[field].(type) {
	case []any:
		targetItems = v
	case nil:
		if len(liveItems) == 0 {
			return false, nil
		}
		target[field] = runtime.DeepCopyJSONValue(liveValue)
		return true, nil
	default:
		return false, fmt.Errorf("%q is not a list in the manifest, mergeKey needs one", field)
	}

	keys := map[string]bool{}
	for _, t := range targetItems {
		if item, ok := t.(map[string]any); ok {
			if k, ok := itemKey(item, mergeKey); ok {
				keys[k] = true
			}
		}
	}

	changed := false
	notObjects := 0
	for _, l := range liveItems {
		item, ok := l.(map[string]any)
		if !ok {
			notObjects++
			continue
		}
		k, ok := itemKey(item, mergeKey)
		if !ok || keys[k] {
			continue
		}
		keys[k] = true
		targetItems = append(targetItems, runtime.DeepCopyJSONValue(item))
		changed = true
	}
	if notObjects > 0 {
		logger.V(1).Info("Not merging live items which are not objects, mergeKey only merges lists of objects",
			"field", field, "mergeKey", mergeKey, "skipped", notObjects)
	}
	if changed {
		target[field] = targetItems
	}

	return changed, nil
}

// itemKey returns the value of field key in item, encoded so that it can be
// compared, and whether item has it.
func itemKey(item map[string]any, key string) (string, bool) {
	v, ok := item[key]
	if !ok || v == nil {
		return "", false
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", false
	}
	return string(b), true
}
