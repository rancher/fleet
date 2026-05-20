package options

import (
	"testing"

	"github.com/stretchr/testify/assert"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

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
	result := Merge(base, edgeCustomization)
	result = Merge(result, extraCustomization)

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

	result := Merge(base, first)
	result = Merge(result, second)

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

	result := Merge(base, first)
	result = Merge(result, second)
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

	result := Merge(base, setForce)
	result = Merge(result, clearForce)
	assert.True(t, result.Helm.Force, "Force cannot be unset once true -- OR semantics")
}
