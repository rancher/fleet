package labelselectors

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Merge combines multiple label selectors by merging match labels and expressions.
// When the same label key appears in multiple selectors with different values, the last value wins.
// Duplicate match expressions are collapsed; conflicting expressions remain and will match nothing.
// Returns nil if all input selectors are nil.
func Merge(selectors ...*metav1.LabelSelector) *metav1.LabelSelector {
	var result *metav1.LabelSelector
	for _, selector := range selectors {
		if selector == nil {
			continue
		}
		if result == nil {
			result = selector.DeepCopy()
		} else {
			if result.MatchLabels == nil {
				result.MatchLabels = make(map[string]string)
			}
			for k, v := range selector.MatchLabels {
				result.MatchLabels[k] = v
			}
			result.MatchExpressions = mergeMatchExpressions(result.MatchExpressions, selector.MatchExpressions)
		}
	}
	return result
}

// mergeMatchExpressions combines two slices of label selector requirements,
// deduplicating identical expressions based on their key, operator, and values.
// Existing expressions are preserved, and only non-duplicate incoming expressions are appended.
func mergeMatchExpressions(existing, incoming []metav1.LabelSelectorRequirement) []metav1.LabelSelectorRequirement {
	if len(incoming) == 0 {
		return existing
	}
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	for _, expr := range existing {
		seen[matchExpressionKey(expr)] = struct{}{}
	}
	for _, expr := range incoming {
		key := matchExpressionKey(expr)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		existing = append(existing, expr)
	}
	return existing
}

// matchExpressionKey generates a unique string key for a label selector requirement
// by concatenating its key, operator, and comma-separated values with "|" delimiter.
func matchExpressionKey(expr metav1.LabelSelectorRequirement) string {
	return strings.Join([]string{expr.Key, string(expr.Operator), strings.Join(expr.Values, ",")}, "|")
}
