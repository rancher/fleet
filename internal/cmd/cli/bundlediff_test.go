package cli

import (
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
