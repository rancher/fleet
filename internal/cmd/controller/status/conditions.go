package status

import (
	"slices"

	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	"k8s.io/apimachinery/pkg/api/equality"
)

// MergeConditions merges the condition list a controller wants to publish into
// the list currently live on the object.
//
// A merge patch replaces the whole conditions array, so a controller which
// rebuilds that array from a snapshot taken at the start of its reconcile
// silently reverts, or outright deletes, conditions another controller wrote
// while it was working. Fleet has several writers per object (the GitRepo and
// HelmOp reconcilers, their polling jobs and their status reconcilers), so that
// window is real: a polling job clearing Stalled can be undone seconds later by
// a reconciler that started before the clear.
//
// Comparing desired against base, the list as the caller first read it, tells
// the two cases apart. A condition type whose value the caller actually changed
// is one it computed, and wins. Every other type is only a stale echo of the
// read, and keeps whatever is live. Types named in foreign are owned by another
// controller entirely and are always taken from live.
//
// Conditions are never removed, only added or updated, so live ordering is
// preserved and types the caller newly computed are appended.
func MergeConditions(
	live, base, desired []genericcondition.GenericCondition,
	foreign ...string,
) []genericcondition.GenericCondition {
	byType := func(conds []genericcondition.GenericCondition) map[string]genericcondition.GenericCondition {
		m := make(map[string]genericcondition.GenericCondition, len(conds))
		for _, c := range conds {
			m[c.Type] = c
		}
		return m
	}

	baseByType := byType(base)
	desiredByType := byType(desired)

	isForeign := func(condType string) bool {
		return slices.Contains(foreign, condType)
	}

	// computed reports whether the caller changed this condition type itself,
	// rather than merely carrying it over from the object it read.
	computed := func(condType string) bool {
		if isForeign(condType) {
			return false
		}
		d, inDesired := desiredByType[condType]
		if !inDesired {
			return false
		}
		b, inBase := baseByType[condType]

		return !inBase || !equality.Semantic.DeepEqual(b, d)
	}

	merged := make([]genericcondition.GenericCondition, 0, len(live)+len(desired))
	seen := make(map[string]struct{}, len(live))

	for _, c := range live {
		seen[c.Type] = struct{}{}
		if computed(c.Type) {
			merged = append(merged, desiredByType[c.Type])
			continue
		}
		merged = append(merged, c)
	}

	// Types the caller computed which do not exist on the live object yet.
	for _, c := range desired {
		if _, ok := seen[c.Type]; ok {
			continue
		}
		if computed(c.Type) {
			merged = append(merged, c)
		}
	}

	return merged
}
