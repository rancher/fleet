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

// ---------- Namespace metadata ----------

func TestMerge_NamespaceMetadata_MergesAndOverrides(t *testing.T) {
	a := assert.New(t)

	base := fleet.BundleDeploymentOptions{
		NamespaceLabels:      map[string]string{"base": "keep"},
		NamespaceAnnotations: map[string]string{"base-ann": "keep"},
	}
	custom := fleet.BundleDeploymentOptions{
		NamespaceLabels:      map[string]string{"region": "eu-west", "base": "override"},
		NamespaceAnnotations: map[string]string{"team": "platform"},
	}

	result := options.Merge(base, custom)

	a.Equal(map[string]string{"base": "override", "region": "eu-west"}, result.NamespaceLabels)
	a.Equal(map[string]string{"base-ann": "keep", "team": "platform"}, result.NamespaceAnnotations)

	// Pure function: inputs must not be modified.
	a.Equal(map[string]string{"base": "keep"}, base.NamespaceLabels)
	a.Equal(map[string]string{"base-ann": "keep"}, base.NamespaceAnnotations)
}

func TestMerge_NamespaceMetadata_EmptyBase(t *testing.T) {
	a := assert.New(t)

	custom := fleet.BundleDeploymentOptions{
		NamespaceLabels:      map[string]string{"region": "eu-west"},
		NamespaceAnnotations: map[string]string{"team": "platform"},
	}

	result := options.Merge(fleet.BundleDeploymentOptions{}, custom)

	a.Equal(custom.NamespaceLabels, result.NamespaceLabels)
	a.Equal(custom.NamespaceAnnotations, result.NamespaceAnnotations)
}

func TestMerge_NamespaceMetadata_EmptyCustom(t *testing.T) {
	a := assert.New(t)

	base := fleet.BundleDeploymentOptions{
		NamespaceLabels:      map[string]string{"base": "keep"},
		NamespaceAnnotations: map[string]string{"base-ann": "keep"},
	}

	result := options.Merge(base, fleet.BundleDeploymentOptions{})

	a.Equal(base.NamespaceLabels, result.NamespaceLabels)
	a.Equal(base.NamespaceAnnotations, result.NamespaceAnnotations)
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

// TestMergeChain verifies that chaining Merge calls (as done in AllMatches mode)
// correctly accumulates values from multiple customizations.
func TestMergeChain(t *testing.T) {
	base := fleet.BundleDeploymentOptions{
		DefaultNamespace: "base-ns",
		Helm: &fleet.HelmOptions{
			ReleaseName: "base-release",
			Values: &fleet.GenericMap{
				Data: map[string]any{
					"source": "base",
					"edge":   "false",
					"extra":  "false",
				},
			},
		},
	}

	edgeCustomization := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{
			Values: &fleet.GenericMap{
				Data: map[string]any{
					"source": "edge",
					"edge":   "true",
				},
			},
		},
	}

	extraCustomization := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{
			Values: &fleet.GenericMap{
				Data: map[string]any{
					"source": "extra",
					"extra":  "true",
				},
			},
		},
	}

	// Simulate AllMatches: base -> edge -> extra
	result := options.Merge(base, edgeCustomization)
	result = options.Merge(result, extraCustomization)

	assert.Equal(t, "base-ns", result.DefaultNamespace, "namespace from base should be preserved")
	assert.Equal(t, "base-release", result.Helm.ReleaseName, "releaseName from base should be preserved")
	assert.Equal(t, "extra", result.Helm.Values.Data["source"], "last matching customization wins for scalar values")
	assert.Equal(t, "true", result.Helm.Values.Data["edge"], "edge value set by first customization should be present")
	assert.Equal(t, "true", result.Helm.Values.Data["extra"], "extra value set by second customization should be present")
}

// TestMergeChain_ValuesFrom verifies that ValuesFrom entries are accumulated
// across multiple customizations.
func TestMergeChain_ValuesFrom(t *testing.T) {
	base := fleet.BundleDeploymentOptions{}

	first := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{
			ValuesFrom: []fleet.ValuesFrom{
				{ConfigMapKeyRef: &fleet.ConfigMapKeySelector{LocalObjectReference: fleet.LocalObjectReference{Name: "cm-edge"}}},
			},
		},
	}

	second := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{
			ValuesFrom: []fleet.ValuesFrom{
				{ConfigMapKeyRef: &fleet.ConfigMapKeySelector{LocalObjectReference: fleet.LocalObjectReference{Name: "cm-extra"}}},
			},
		},
	}

	result := options.Merge(base, first)
	result = options.Merge(result, second)

	assert.Len(t, result.Helm.ValuesFrom, 2, "ValuesFrom entries should be accumulated")
	assert.Equal(t, "cm-edge", result.Helm.ValuesFrom[0].ConfigMapKeyRef.Name)
	assert.Equal(t, "cm-extra", result.Helm.ValuesFrom[1].ConfigMapKeyRef.Name)
}

// TestMergeChain_ScalarLastWins verifies that for scalar fields (namespace,
// releaseName) the last non-empty customization wins.
func TestMergeChain_ScalarLastWins(t *testing.T) {
	base := fleet.BundleDeploymentOptions{
		DefaultNamespace: "base-ns",
	}
	first := fleet.BundleDeploymentOptions{
		DefaultNamespace: "first-ns",
	}
	second := fleet.BundleDeploymentOptions{
		DefaultNamespace: "second-ns",
	}

	result := options.Merge(base, first)
	result = options.Merge(result, second)
	assert.Equal(t, "second-ns", result.DefaultNamespace, "last non-empty namespace wins")
}

// TestMergeChain_BoolSticky verifies that OR'd booleans are sticky once set true.
//
// TODO we might want to rethink that approach, but for now this verifies that
// if one customization sets Force to true, it cannot be unset by a later
// customization.
func TestMergeChain_BoolSticky(t *testing.T) {
	base := fleet.BundleDeploymentOptions{}
	setForce := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{Force: true},
	}
	clearForce := fleet.BundleDeploymentOptions{
		Helm: &fleet.HelmOptions{Force: false},
	}

	result := options.Merge(base, setForce)
	result = options.Merge(result, clearForce)
	assert.True(t, result.Helm.Force, "Force cannot be unset once true -- OR semantics")
}
