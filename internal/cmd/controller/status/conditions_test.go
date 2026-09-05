package status

import (
	"testing"

	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	corev1 "k8s.io/api/core/v1"
)

func cond(condType string, status corev1.ConditionStatus, message string) genericcondition.GenericCondition {
	return genericcondition.GenericCondition{Type: condType, Status: status, Message: message}
}

func types(conds []genericcondition.GenericCondition) []string {
	out := make([]string, 0, len(conds))
	for _, c := range conds {
		out = append(out, c.Type)
	}
	return out
}

func find(conds []genericcondition.GenericCondition, condType string) (genericcondition.GenericCondition, bool) {
	for _, c := range conds {
		if c.Type == condType {
			return c, true
		}
	}
	return genericcondition.GenericCondition{}, false
}

func TestMergeConditions(t *testing.T) {
	cases := []struct {
		name     string
		live     []genericcondition.GenericCondition
		base     []genericcondition.GenericCondition
		desired  []genericcondition.GenericCondition
		foreign  []string
		expected []genericcondition.GenericCondition
	}{
		{
			name:     "a condition the caller never saw is kept",
			live:     []genericcondition.GenericCondition{cond("Polled", corev1.ConditionTrue, "fresh")},
			base:     nil,
			desired:  nil,
			expected: []genericcondition.GenericCondition{cond("Polled", corev1.ConditionTrue, "fresh")},
		},
		{
			// The core regression: the caller read Polled=False, another writer
			// then set it to True. The caller did not touch it, so its stale copy
			// must not win.
			name:     "a condition the caller carried over unchanged keeps the live value",
			live:     []genericcondition.GenericCondition{cond("Polled", corev1.ConditionTrue, "fresh")},
			base:     []genericcondition.GenericCondition{cond("Polled", corev1.ConditionFalse, "stale")},
			desired:  []genericcondition.GenericCondition{cond("Polled", corev1.ConditionFalse, "stale")},
			expected: []genericcondition.GenericCondition{cond("Polled", corev1.ConditionTrue, "fresh")},
		},
		{
			name:     "a condition the caller changed wins over the live value",
			live:     []genericcondition.GenericCondition{cond("Accepted", corev1.ConditionTrue, "old")},
			base:     []genericcondition.GenericCondition{cond("Accepted", corev1.ConditionTrue, "old")},
			desired:  []genericcondition.GenericCondition{cond("Accepted", corev1.ConditionFalse, "computed")},
			expected: []genericcondition.GenericCondition{cond("Accepted", corev1.ConditionFalse, "computed")},
		},
		{
			name:     "a condition the caller newly computed is appended",
			live:     []genericcondition.GenericCondition{cond("Polled", corev1.ConditionTrue, "fresh")},
			base:     nil,
			desired:  []genericcondition.GenericCondition{cond("Accepted", corev1.ConditionTrue, "computed")},
			expected: []genericcondition.GenericCondition{cond("Polled", corev1.ConditionTrue, "fresh"), cond("Accepted", corev1.ConditionTrue, "computed")},
		},
		{
			name:     "a stale condition absent from live is not resurrected",
			live:     nil,
			base:     []genericcondition.GenericCondition{cond("Polled", corev1.ConditionTrue, "stale")},
			desired:  []genericcondition.GenericCondition{cond("Polled", corev1.ConditionTrue, "stale")},
			expected: nil,
		},
		{
			name:     "a foreign condition is always taken from live",
			live:     []genericcondition.GenericCondition{cond("Ready", corev1.ConditionTrue, "fresh")},
			base:     []genericcondition.GenericCondition{cond("Ready", corev1.ConditionFalse, "stale")},
			desired:  []genericcondition.GenericCondition{cond("Ready", corev1.ConditionFalse, "stale-changed")},
			foreign:  []string{"Ready"},
			expected: []genericcondition.GenericCondition{cond("Ready", corev1.ConditionTrue, "fresh")},
		},
		{
			name:     "a foreign condition missing from live is not added",
			live:     nil,
			base:     nil,
			desired:  []genericcondition.GenericCondition{cond("Ready", corev1.ConditionTrue, "computed")},
			foreign:  []string{"Ready"},
			expected: nil,
		},
		{
			name: "live ordering is preserved",
			live: []genericcondition.GenericCondition{
				cond("Ready", corev1.ConditionTrue, ""),
				cond("Polled", corev1.ConditionTrue, "fresh"),
				cond("Accepted", corev1.ConditionTrue, "old"),
			},
			base: []genericcondition.GenericCondition{
				cond("Polled", corev1.ConditionFalse, "stale"),
				cond("Accepted", corev1.ConditionTrue, "old"),
			},
			desired: []genericcondition.GenericCondition{
				cond("Polled", corev1.ConditionFalse, "stale"),
				cond("Accepted", corev1.ConditionFalse, "computed"),
				cond("Stalled", corev1.ConditionFalse, "computed"),
			},
			foreign: []string{"Ready"},
			expected: []genericcondition.GenericCondition{
				cond("Ready", corev1.ConditionTrue, ""),
				cond("Polled", corev1.ConditionTrue, "fresh"),
				cond("Accepted", corev1.ConditionFalse, "computed"),
				cond("Stalled", corev1.ConditionFalse, "computed"),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MergeConditions(tc.live, tc.base, tc.desired, tc.foreign...)

			if len(got) != len(tc.expected) {
				t.Fatalf("expected conditions %v, got %v", types(tc.expected), types(got))
			}
			for i, want := range tc.expected {
				if got[i].Type != want.Type {
					t.Fatalf("expected conditions %v, got %v", types(tc.expected), types(got))
				}
				if got[i] != want {
					t.Errorf("condition %q: expected %+v, got %+v", want.Type, want, got[i])
				}
			}
		})
	}
}

func TestMergeConditionsDoesNotAliasInputs(t *testing.T) {
	live := []genericcondition.GenericCondition{cond("Polled", corev1.ConditionTrue, "fresh")}
	desired := []genericcondition.GenericCondition{cond("Accepted", corev1.ConditionTrue, "computed")}

	merged := MergeConditions(live, nil, desired)

	accepted, ok := find(merged, "Accepted")
	if !ok {
		t.Fatal("expected the newly computed Accepted condition to be merged in")
	}
	accepted.Message = "mutated"

	if desired[0].Message != "computed" {
		t.Errorf("mutating the merged result changed the desired input: %+v", desired[0])
	}
	if len(live) != 1 {
		t.Errorf("merging appended to the live slice: %+v", live)
	}
}
