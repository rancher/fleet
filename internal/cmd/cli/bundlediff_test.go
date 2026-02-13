package cli

import (
	"testing"

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
			JsonPointers: []string{
				"/data/a",
			},
		},
	}

	newPatches := []fleet.ComparePatch{
		{
			APIVersion: "v1",
			Kind:       "ConfigMap",
			Name:       "app-config",
			Namespace:  "default",
			Operations: []fleet.Operation{{Op: "remove", Path: "/data/b"}},
			JsonPointers: []string{
				"/data/b",
			},
		},
		{
			APIVersion: "v1",
			Kind:       "Secret",
			Name:       "app-secret",
			Namespace:  "default",
			Operations: []fleet.Operation{{Op: "remove", Path: "/data/key"}},
			JsonPointers: []string{
				"/data/key",
			},
		},
	}

	merged := mergeComparePatches(existing, newPatches)

	if len(merged) != 2 {
		t.Fatalf("expected 2 merged patches, got %d", len(merged))
	}

	var configPatch *fleet.ComparePatch
	for i := range merged {
		if merged[i].Kind == "ConfigMap" && merged[i].Name == "app-config" {
			configPatch = &merged[i]
			break
		}
	}
	if configPatch == nil {
		t.Fatalf("expected ConfigMap patch to exist")
	}
	if len(configPatch.Operations) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(configPatch.Operations))
	}
	if len(configPatch.JsonPointers) != 2 {
		t.Fatalf("expected 2 jsonPointers, got %d", len(configPatch.JsonPointers))
	}
}
