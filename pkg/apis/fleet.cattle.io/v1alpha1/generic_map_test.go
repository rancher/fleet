package v1alpha1

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/equality"
)

func Test_deepCopyMap(t *testing.T) {
	type fixture func(map[string]any)
	tests := []struct {
		name string
		src  map[string]interface{}
		// perform changes after creation and ensure the original does not change
		fixtures []fixture
	}{
		{name: "copies top-level keys", src: map[string]interface{}{
			"str":     "value",
			"list":    []any{1, "dos", true},
			"boolean": false,
		}},
		{name: "copies nested values", src: map[string]interface{}{
			"first": map[string]any{
				"second": map[string]any{
					"third": map[string]any{
						"str":     "value",
						"boolean": false,
					},
					"list":    []any{1, "dos", true},
					"boolean": false,
				},
			},
		}},
		{name: "changing top-level values preserves the original map", src: map[string]interface{}{
			"str":      "value",
			"number":   3,
			"boolean":  false,
			"todelete": "foo",
		}, fixtures: []fixture{
			func(m map[string]interface{}) {
				m["str"] = "anotherValue"
			},
			func(m map[string]interface{}) {
				m["number"] = "anotherValue"
			},
			func(m map[string]interface{}) {
				m["boolean"] = true
			},
			func(m map[string]interface{}) {
				m["boolean"] = "notanymore"
			},
			func(m map[string]interface{}) {
				delete(m, "todelete")
			},
		}},
		{name: "modifying maps inside a slice preserves the original map", src: map[string]interface{}{
			"list": []any{map[string]any{
				"original": "value",
			}},
		}, fixtures: []fixture{
			func(m map[string]interface{}) {
				list := m["list"].([]any)
				mapInList := list[0].(map[string]any)
				mapInList["original"] = "newvalue"
			},
			func(m map[string]interface{}) {
				list := m["list"].([]any)
				mapInList := list[0].(map[string]any)
				mapInList["modified"] = "another"
			},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := map[string]interface{}{}
			deepCopyMap(tt.src, res)

			if got, want := res, tt.src; !equality.Semantic.DeepEqual(got, want) {
				t.Errorf("the produced copy is not identical, got: %s, want: %s", got, want)
			}

			for _, modify := range tt.fixtures {
				modify(res)
				if equality.Semantic.DeepEqual(tt.src, res) {
					t.Errorf("original was modified after modifying the copy")
				}
				// Apply same modifications to the original after comparing, to only test the delta
				modify(tt.src)
			}
		})
	}
}
