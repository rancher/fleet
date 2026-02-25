package cli

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestMergeComparePatches(t *testing.T) {
	existing := []fleet.ComparePatch{
		{
			APIVersion: "v1",
			Kind:       "ConfigMap",
			Name:       "app-config",
			Namespace:  "default",
			Operations: []fleet.Operation{{Op: "remove", Path: "/data/a"}},
		},
	}

	newPatches := []fleet.ComparePatch{
		{
			APIVersion: "v1",
			Kind:       "ConfigMap",
			Name:       "app-config",
			Namespace:  "default",
			Operations: []fleet.Operation{{Op: "remove", Path: "/data/b"}},
		},
		{
			APIVersion: "v1",
			Kind:       "Secret",
			Name:       "app-secret",
			Namespace:  "default",
			Operations: []fleet.Operation{{Op: "remove", Path: "/data/key"}},
		},
	}

	expected := []fleet.ComparePatch{
		{
			APIVersion: "v1",
			Kind:       "ConfigMap",
			Name:       "app-config",
			Namespace:  "default",
			Operations: []fleet.Operation{
				{Op: "remove", Path: "/data/a"},
				{Op: "remove", Path: "/data/b"},
			},
		},
		{
			APIVersion: "v1",
			Kind:       "Secret",
			Name:       "app-secret",
			Namespace:  "default",
			Operations: []fleet.Operation{{Op: "remove", Path: "/data/key"}},
		},
	}

	merged := mergeComparePatches(existing, newPatches)

	if diff := cmp.Diff(expected, merged); diff != "" {
		t.Errorf("mergeComparePatches() mismatch (-want +got):\n%s", diff)
	}
}

func TestConvertMergePatchToRemoveOps(t *testing.T) {
	tests := []struct {
		name     string
		patch    string
		basePath string
		want     []patchOperation
	}{
		{
			name:     "null leaf generates remove",
			patch:    `{"data":null}`,
			basePath: "",
			want: []patchOperation{
				{Op: "remove", Path: "/data"},
			},
		},
		{
			name:     "non-null leaf generates remove",
			patch:    `{"data":{"key":"value"}}`,
			basePath: "",
			want: []patchOperation{
				{Op: "remove", Path: "/data/key"},
			},
		},
		{
			name:     "empty object generates remove for parent path",
			patch:    `{"data":{}}`,
			basePath: "",
			want: []patchOperation{
				{Op: "remove", Path: "/data"},
			},
		},
		{
			name:     "nested empty object generates remove for its path",
			patch:    `{"spec":{"template":{"metadata":{}}}}`,
			basePath: "",
			want: []patchOperation{
				{Op: "remove", Path: "/spec/template/metadata"},
			},
		},
		{
			name:     "mixed null and empty object",
			patch:    `{"data":{},"metadata":null}`,
			basePath: "",
			want: []patchOperation{
				{Op: "remove", Path: "/data"},
				{Op: "remove", Path: "/metadata"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var mergePatch map[string]interface{}
			if err := json.Unmarshal([]byte(tc.patch), &mergePatch); err != nil {
				t.Fatalf("failed to parse patch: %v", err)
			}

			got := convertMergePatchToRemoveOps(mergePatch, tc.basePath)

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("convertMergePatchToRemoveOps() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
