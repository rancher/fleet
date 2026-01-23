package labelselectors

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMerge(t *testing.T) {
	tests := []struct {
		name      string
		selectors []*metav1.LabelSelector
		want      *metav1.LabelSelector
	}{
		{
			name:      "nil selectors returns nil",
			selectors: nil,
			want:      nil,
		},
		{
			name:      "empty slice returns nil",
			selectors: []*metav1.LabelSelector{},
			want:      nil,
		},
		{
			name:      "all nil selectors returns nil",
			selectors: []*metav1.LabelSelector{nil, nil},
			want:      nil,
		},
		{
			name: "single nil selector with valid selector",
			selectors: []*metav1.LabelSelector{
				nil,
				{
					MatchLabels: map[string]string{"env": "prod"},
				},
			},
			want: &metav1.LabelSelector{
				MatchLabels: map[string]string{"env": "prod"},
			},
		},
		{
			name: "single selector is deep copied",
			selectors: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"app": "test"},
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"frontend"}},
					},
				},
			},
			want: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test"},
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"frontend"}},
				},
			},
		},
		{
			name: "merge non-conflicting MatchLabels",
			selectors: []*metav1.LabelSelector{
				{MatchLabels: map[string]string{"app": "web"}},
				{MatchLabels: map[string]string{"env": "prod"}},
			},
			want: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "web",
					"env": "prod",
				},
			},
		},
		{
			name: "conflicting MatchLabels - last value wins",
			selectors: []*metav1.LabelSelector{
				{MatchLabels: map[string]string{"env": "dev"}},
				{MatchLabels: map[string]string{"env": "prod"}},
			},
			want: &metav1.LabelSelector{
				MatchLabels: map[string]string{"env": "prod"},
			},
		},
		{
			name: "merge MatchExpressions",
			selectors: []*metav1.LabelSelector{
				{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"frontend"}},
					},
				},
				{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{Key: "env", Operator: metav1.LabelSelectorOpNotIn, Values: []string{"test"}},
					},
				},
			},
			want: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"frontend"}},
					{Key: "env", Operator: metav1.LabelSelectorOpNotIn, Values: []string{"test"}},
				},
			},
		},
		{
			name: "merge both MatchLabels and MatchExpressions",
			selectors: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"app": "web"},
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"frontend"}},
					},
				},
				{
					MatchLabels: map[string]string{"env": "prod"},
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{Key: "region", Operator: metav1.LabelSelectorOpExists},
					},
				},
			},
			want: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "web",
					"env": "prod",
				},
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"frontend"}},
					{Key: "region", Operator: metav1.LabelSelectorOpExists},
				},
			},
		},
		{
			name: "multiple selectors with nil in between",
			selectors: []*metav1.LabelSelector{
				{MatchLabels: map[string]string{"a": "1"}},
				nil,
				{MatchLabels: map[string]string{"b": "2"}},
			},
			want: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"a": "1",
					"b": "2",
				},
			},
		},
		{
			name: "complex merge with overlapping keys",
			selectors: []*metav1.LabelSelector{
				{
					MatchLabels: map[string]string{"app": "web", "env": "dev"},
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"frontend"}},
					},
				},
				{
					MatchLabels: map[string]string{"env": "prod", "region": "us-west"},
				},
				{
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{Key: "version", Operator: metav1.LabelSelectorOpExists},
					},
				},
			},
			want: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":    "web",
					"env":    "prod",
					"region": "us-west",
				},
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"frontend"}},
					{Key: "version", Operator: metav1.LabelSelectorOpExists},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Merge(tt.selectors...)

			if tt.want == nil {
				if got != nil {
					t.Errorf("Merge() = %v, want nil", got)
				}
				return
			}

			if got == nil {
				t.Errorf("Merge() = nil, want %v", tt.want)
				return
			}

			if !equalMatchLabels(got.MatchLabels, tt.want.MatchLabels) {
				t.Errorf("Merge() MatchLabels = %v, want %v", got.MatchLabels, tt.want.MatchLabels)
			}

			if !equalMatchExpressions(got.MatchExpressions, tt.want.MatchExpressions) {
				t.Errorf("Merge() MatchExpressions = %v, want %v", got.MatchExpressions, tt.want.MatchExpressions)
			}
		})
	}
}

func TestMerge_DeepCopy(t *testing.T) {
	original := &metav1.LabelSelector{
		MatchLabels: map[string]string{"app": "test"},
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{Key: "env", Operator: metav1.LabelSelectorOpIn, Values: []string{"prod"}},
		},
	}

	result := Merge(original)

	if result.MatchLabels["app"] != "test" {
		t.Errorf("Merge() did not copy MatchLabels correctly")
	}

	result.MatchLabels["app"] = "modified"
	if original.MatchLabels["app"] != "test" {
		t.Errorf("Merge() did not deep copy MatchLabels, original was modified")
	}

	result.MatchExpressions[0].Key = "modified"
	if original.MatchExpressions[0].Key != "env" {
		t.Errorf("Merge() did not deep copy MatchExpressions, original was modified")
	}
}

func equalMatchLabels(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func equalMatchExpressions(a, b []metav1.LabelSelectorRequirement) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Operator != b[i].Operator {
			return false
		}
		if !equalStringSlices(a[i].Values, b[i].Values) {
			return false
		}
	}
	return true
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
