package desiredset

import (
	"encoding/json"
	"testing"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/objectset"
)

// Test_Diff_NullPatch validates normalizeNullPatch behavior across various
// scenarios including nested nulls, arrays with nulls, and edge cases.
func Test_Diff_NullPatch(t *testing.T) {
	key := objectset.ObjectKey{Name: "test", Namespace: "ns"}
	tests := []struct {
		name        string
		patch       string
		expectPatch string
		expectEmpty bool
		expectErr   bool
	}{
		{
			name:        "keeps_patch_without_nulls",
			patch:       `{"metadata":{"labels":{"a":"b"}}}`,
			expectPatch: `{"metadata":{"labels":{"a":"b"}}}`,
		},
		{
			name:        "removes_null_field",
			patch:       `{"spec":{"strategy":{"rollingUpdate":null,"type":"RollingUpdate"}}}`,
			expectPatch: `{"spec":{"strategy":{"type":"RollingUpdate"}}}`,
		},
		{
			name:        "removes_nested_null_fields",
			patch:       `{"spec":{"template":{"spec":{"securityContext":null,"containers":[{"name":"c1","image":"nginx"}]}}}}`,
			expectPatch: `{"spec":{"template":{"spec":{"containers":[{"name":"c1","image":"nginx"}]}}}}`,
		},
		{
			name:        "removes_null_elements_from_arrays",
			patch:       `{"spec":{"tolerations":[{"key":"a","operator":null},null,{"key":"b"}]}}`,
			expectPatch: `{"spec":{"tolerations":[{"key":"a"},{"key":"b"}]}}`,
		},
		{
			name:        "removes_multiple_nulls_across_tree",
			patch:       `{"spec":{"foo":null,"bar":{"baz":null,"keep":"x"}},"metadata":{"annotations":null}}`,
			expectPatch: `{"spec":{"bar":{"keep":"x"}}}`,
		},
		{
			name:        "empties_patch_when_only_nulls",
			patch:       `{"spec":{"strategy":{"rollingUpdate":null}}}`,
			expectEmpty: true,
		},
		{
			name:      "fails_on_malformed_json",
			patch:     `{"spec":`,
			expectErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			patch := []byte(tc.patch)
			emptied, err := normalizeNullPatch(key, &patch)

			if tc.expectErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if emptied != tc.expectEmpty {
				t.Fatalf("emptied = %v, want %v", emptied, tc.expectEmpty)
			}

			if tc.expectEmpty {
				return
			}

			assertPatchJSONEqual(t, string(patch), tc.expectPatch)
		})
	}
}

// Test_Diff_RemoveNullPatchFields validates the recursive null removal logic
// with a complex nested structure containing maps, arrays, and null values.
func Test_Diff_RemoveNullPatchFields(t *testing.T) {
	input := map[string]any{
		"spec": map[string]any{
			"list": []any{
				map[string]any{"name": "a", "value": nil},
				nil,
				"text",
			},
			"empty": map[string]any{"foo": nil},
		},
		"metadata": map[string]any{"labels": map[string]any{"x": "y"}},
	}

	cleanedAny := removeNullPatchFields(input)
	cleaned, ok := cleanedAny.(map[string]any)
	if !ok {
		t.Fatalf("cleaned type = %T, want map[string]any", cleanedAny)
	}

	expected := map[string]any{
		"spec": map[string]any{
			"list": []any{
				map[string]any{"name": "a"},
				"text",
			},
		},
		"metadata": map[string]any{"labels": map[string]any{"x": "y"}},
	}

	gotJSON, err := json.Marshal(cleaned)
	if err != nil {
		t.Fatalf("failed to marshal cleaned: %v", err)
	}
	wantJSON, err := json.Marshal(expected)
	if err != nil {
		t.Fatalf("failed to marshal expected: %v", err)
	}

	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("cleaned mismatch\ngot:  %s\nwant: %s", gotJSON, wantJSON)
	}
}

// assertPatchJSONEqual compares two JSON strings for semantic equality,
// normalizing formatting differences through unmarshal/marshal cycles.
func assertPatchJSONEqual(t *testing.T, got, want string) {
	t.Helper()

	var gotObj map[string]any
	if err := json.Unmarshal([]byte(got), &gotObj); err != nil {
		t.Fatalf("failed to unmarshal got json: %v", err)
	}

	var wantObj map[string]any
	if err := json.Unmarshal([]byte(want), &wantObj); err != nil {
		t.Fatalf("failed to unmarshal want json: %v", err)
	}

	gotNorm, err := json.Marshal(gotObj)
	if err != nil {
		t.Fatalf("failed to marshal got object: %v", err)
	}
	wantNorm, err := json.Marshal(wantObj)
	if err != nil {
		t.Fatalf("failed to marshal want object: %v", err)
	}

	if string(gotNorm) != string(wantNorm) {
		t.Fatalf("json mismatch\ngot:  %s\nwant: %s", gotNorm, wantNorm)
	}
}
