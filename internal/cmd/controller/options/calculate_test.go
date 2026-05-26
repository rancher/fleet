package options_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rancher/fleet/internal/cmd/controller/options"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// ---------- DownstreamResources ----------

func TestMerge_DownstreamResources_Appends(t *testing.T) {
	a := assert.New(t)

	base := fleet.BundleDeploymentOptions{
		DownstreamResources: []fleet.DownstreamResource{
			{Kind: "Secret", Name: "base-secret"},
		},
	}
	custom := fleet.BundleDeploymentOptions{
		DownstreamResources: []fleet.DownstreamResource{
			{Kind: "ConfigMap", Name: "target-cm"},
		},
	}

	result := options.Merge(base, custom)
	a.Len(result.DownstreamResources, 2)
	a.Equal(fleet.DownstreamResource{Kind: "Secret", Name: "base-secret"}, result.DownstreamResources[0])
	a.Equal(fleet.DownstreamResource{Kind: "ConfigMap", Name: "target-cm"}, result.DownstreamResources[1])

	// Pure function: inputs must not be modified.
	a.Len(base.DownstreamResources, 1)
	a.Len(custom.DownstreamResources, 1)
}

func TestMerge_DownstreamResources_DeduplicatesExact(t *testing.T) {
	a := assert.New(t)

	base := fleet.BundleDeploymentOptions{
		DownstreamResources: []fleet.DownstreamResource{
			{Kind: "Secret", Name: "shared"},
		},
	}
	custom := fleet.BundleDeploymentOptions{
		DownstreamResources: []fleet.DownstreamResource{
			{Kind: "Secret", Name: "shared"},
		},
	}

	result := options.Merge(base, custom)
	a.Len(result.DownstreamResources, 1, "duplicate should be dropped")
	a.Equal(fleet.DownstreamResource{Kind: "Secret", Name: "shared"}, result.DownstreamResources[0])
}

func TestMerge_DownstreamResources_DeduplicatesCaseInsensitiveKind(t *testing.T) {
	a := assert.New(t)

	base := fleet.BundleDeploymentOptions{
		DownstreamResources: []fleet.DownstreamResource{
			{Kind: "Secret", Name: "sec"},
		},
	}
	custom := fleet.BundleDeploymentOptions{
		DownstreamResources: []fleet.DownstreamResource{
			{Kind: "secret", Name: "sec"},
		},
	}

	result := options.Merge(base, custom)
	a.Len(result.DownstreamResources, 1, "kind comparison must be case-insensitive")
}

func TestMerge_DownstreamResources_EmptyCustom(t *testing.T) {
	a := assert.New(t)

	base := fleet.BundleDeploymentOptions{
		DownstreamResources: []fleet.DownstreamResource{
			{Kind: "Secret", Name: "base-secret"},
		},
	}

	result := options.Merge(base, fleet.BundleDeploymentOptions{})
	a.Equal(base.DownstreamResources, result.DownstreamResources)
}

func TestMerge_DownstreamResources_EmptyBase(t *testing.T) {
	a := assert.New(t)

	custom := fleet.BundleDeploymentOptions{
		DownstreamResources: []fleet.DownstreamResource{
			{Kind: "Secret", Name: "target-secret"},
		},
	}

	result := options.Merge(fleet.BundleDeploymentOptions{}, custom)
	a.Equal(custom.DownstreamResources, result.DownstreamResources)
}

// ---------- ValuesFrom ----------

func TestMerge_ValuesFrom_Appends(t *testing.T) {
	a := assert.New(t)

	base := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{
			ValuesFrom: []fleet.ValuesFrom{
				{ConfigMapKeyRef: &fleet.ConfigMapKeySelector{
					LocalObjectReference: fleet.LocalObjectReference{Name: "base-cm"},
					Namespace:            "ns",
					Key:                  "values.yaml",
				}},
			},
		},
	}
	custom := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{
			ValuesFrom: []fleet.ValuesFrom{
				{SecretKeyRef: &fleet.SecretKeySelector{
					LocalObjectReference: fleet.LocalObjectReference{Name: "custom-sec"},
					Namespace:            "ns",
					Key:                  "values.yaml",
				}},
			},
		},
	}

	result := options.Merge(base, custom)
	a.Len(result.Helm.ValuesFrom, 2)
}

func TestMerge_ValuesFrom_DeduplicatesConfigMap(t *testing.T) {
	a := assert.New(t)

	vf := fleet.ValuesFrom{ConfigMapKeyRef: &fleet.ConfigMapKeySelector{
		LocalObjectReference: fleet.LocalObjectReference{Name: "cm"},
		Namespace:            "ns",
		Key:                  "values.yaml",
	}}
	base := fleet.BundleDeploymentOptions{Helm: &fleet.HelmOptions{ValuesFrom: []fleet.ValuesFrom{vf}}}
	custom := fleet.BundleDeploymentOptions{Helm: &fleet.HelmOptions{ValuesFrom: []fleet.ValuesFrom{vf}}}

	result := options.Merge(base, custom)
	a.Len(result.Helm.ValuesFrom, 1, "identical ValuesFrom entry should be deduplicated")
}

func TestMerge_ValuesFrom_DeduplicatesSecret(t *testing.T) {
	a := assert.New(t)

	vf := fleet.ValuesFrom{SecretKeyRef: &fleet.SecretKeySelector{
		LocalObjectReference: fleet.LocalObjectReference{Name: "sec"},
		Namespace:            "ns",
		Key:                  "values.yaml",
	}}
	base := fleet.BundleDeploymentOptions{Helm: &fleet.HelmOptions{ValuesFrom: []fleet.ValuesFrom{vf}}}
	custom := fleet.BundleDeploymentOptions{Helm: &fleet.HelmOptions{ValuesFrom: []fleet.ValuesFrom{vf}}}

	result := options.Merge(base, custom)
	a.Len(result.Helm.ValuesFrom, 1, "identical ValuesFrom entry should be deduplicated")
}

func TestMerge_ValuesFrom_SameNameDifferentKey(t *testing.T) {
	a := assert.New(t)

	// Same configmap, different key — these are distinct sources and must both be kept.
	base := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{
			ValuesFrom: []fleet.ValuesFrom{
				{ConfigMapKeyRef: &fleet.ConfigMapKeySelector{
					LocalObjectReference: fleet.LocalObjectReference{Name: "cm"},
					Namespace:            "ns",
					Key:                  "key1",
				}},
			},
		},
	}
	custom := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{
			ValuesFrom: []fleet.ValuesFrom{
				{ConfigMapKeyRef: &fleet.ConfigMapKeySelector{
					LocalObjectReference: fleet.LocalObjectReference{Name: "cm"},
					Namespace:            "ns",
					Key:                  "key2",
				}},
			},
		},
	}

	result := options.Merge(base, custom)
	a.Len(result.Helm.ValuesFrom, 2, "same name but different key should be treated as distinct sources")
}

// ---------- ComparePatches ----------

func TestMerge_ComparePatches_Appends(t *testing.T) {
	a := assert.New(t)

	base := fleet.BundleDeploymentOptions{
		Diff: &fleet.DiffOptions{
			ComparePatches: []fleet.ComparePatch{
				{APIVersion: "v1", Kind: "Secret", Namespace: "ns", Name: "sec"},
			},
		},
	}
	custom := fleet.BundleDeploymentOptions{
		Diff: &fleet.DiffOptions{
			ComparePatches: []fleet.ComparePatch{
				{APIVersion: "v1", Kind: "ConfigMap", Namespace: "ns", Name: "cm"},
			},
		},
	}

	result := options.Merge(base, custom)
	a.Len(result.Diff.ComparePatches, 2)
}

func TestMerge_ComparePatches_CustomTakesPrecedence(t *testing.T) {
	a := assert.New(t)

	baseOp := fleet.Operation{Op: "remove", Path: "/metadata/labels/base"}
	customOp := fleet.Operation{Op: "remove", Path: "/metadata/labels/custom"}
	resource := fleet.ComparePatch{APIVersion: "v1", Kind: "Secret", Namespace: "ns", Name: "shared"}

	base := fleet.BundleDeploymentOptions{
		Diff: &fleet.DiffOptions{
			ComparePatches: []fleet.ComparePatch{
				{APIVersion: resource.APIVersion, Kind: resource.Kind, Namespace: resource.Namespace, Name: resource.Name,
					Operations: []fleet.Operation{baseOp}},
			},
		},
	}
	custom := fleet.BundleDeploymentOptions{
		Diff: &fleet.DiffOptions{
			ComparePatches: []fleet.ComparePatch{
				{APIVersion: resource.APIVersion, Kind: resource.Kind, Namespace: resource.Namespace, Name: resource.Name,
					Operations: []fleet.Operation{customOp}},
			},
		},
	}

	result := options.Merge(base, custom)
	a.Len(result.Diff.ComparePatches, 1, "overlapping patch should not be duplicated")
	a.Equal([]fleet.Operation{customOp}, result.Diff.ComparePatches[0].Operations,
		"custom operations should take precedence over base")
}

func TestMerge_ComparePatches_EmptyCustom(t *testing.T) {
	a := assert.New(t)

	base := fleet.BundleDeploymentOptions{
		Diff: &fleet.DiffOptions{
			ComparePatches: []fleet.ComparePatch{
				{APIVersion: "v1", Kind: "Secret", Namespace: "ns", Name: "sec"},
			},
		},
	}

	result := options.Merge(base, fleet.BundleDeploymentOptions{})
	a.Equal(base.Diff.ComparePatches, result.Diff.ComparePatches)
}
