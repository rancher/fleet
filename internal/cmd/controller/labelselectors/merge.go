package labelselectors

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Merge combines multiple label selectors by merging match labels and expressions.
// When the same label key appears in multiple selectors with different values, the last value wins.
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
			result.MatchExpressions = append(result.MatchExpressions, selector.MatchExpressions...)
		}
	}
	return result
}
