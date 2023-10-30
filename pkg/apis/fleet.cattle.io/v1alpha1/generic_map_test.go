package v1alpha1

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/equality"
)

func Test_deepCopyMap(t *testing.T) {
	tests := []struct {
		name string
		src  map[string]interface{}
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := map[string]interface{}{}
			deepCopyMap(tt.src, res)
			if !equality.Semantic.DeepEqual(tt.src, res) {
				t.Errorf("result object is not identical: %+v", res)
			}
		})
	}
}
