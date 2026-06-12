package matcher

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// makeBundle builds a minimal Bundle reflecting the order produced by bundlereader:
// targetCustomizations come first in Targets, followed by the GitRepo target.
// The GitRepo target is also added as a TargetRestriction.
func makeBundle(gitRepoTarget fleet.BundleTarget, customizations []fleet.BundleTarget) *fleet.Bundle {
	targets := make([]fleet.BundleTarget, 0, len(customizations)+1)
	targets = append(targets, customizations...)
	targets = append(targets, gitRepoTarget)
	b := &fleet.Bundle{
		Spec: fleet.BundleSpec{
			Targets: targets,
			TargetRestrictions: []fleet.BundleTargetRestriction{
				{
					Name:            gitRepoTarget.Name,
					ClusterSelector: gitRepoTarget.ClusterSelector,
				},
			},
		},
	}
	return b
}

func labelSelector(labels map[string]string) *metav1.LabelSelector {
	return &metav1.LabelSelector{MatchLabels: labels}
}

// TestMatchAllTargetCustomizations_NoGroups verifies that MatchAllTargetCustomizations
// returns all matching customizations (without restrictions) in order.
func TestMatchAllTargetCustomizations_NoGroups(t *testing.T) {
	// gitRepoTarget matches all clusters (catch-all selector), simulating a GitRepo target.
	gitRepoTarget := fleet.BundleTarget{Name: "gitrepo", ClusterSelector: &metav1.LabelSelector{}}

	tests := []struct {
		name                 string
		customizationTargets []fleet.BundleTarget
		clusterLabels        map[string]string
		wantNames            []string
	}{
		{
			name: "no customizations match",
			customizationTargets: []fleet.BundleTarget{
				{Name: "edge", ClusterSelector: labelSelector(map[string]string{"edge": "true"})},
			},
			clusterLabels: map[string]string{},
			wantNames:     nil,
		},
		{
			name: "one customization matches",
			customizationTargets: []fleet.BundleTarget{
				{Name: "edge", ClusterSelector: labelSelector(map[string]string{"edge": "true"})},
				{Name: "extra", ClusterSelector: labelSelector(map[string]string{"extra": "true"})},
			},
			clusterLabels: map[string]string{"edge": "true"},
			wantNames:     []string{"edge"},
		},
		{
			name: "both customizations match",
			customizationTargets: []fleet.BundleTarget{
				{Name: "edge", ClusterSelector: labelSelector(map[string]string{"edge": "true"})},
				{Name: "extra", ClusterSelector: labelSelector(map[string]string{"extra": "true"})},
			},
			clusterLabels: map[string]string{"edge": "true", "extra": "true"},
			wantNames:     []string{"edge", "extra"},
		},
		{
			name: "order is preserved",
			customizationTargets: []fleet.BundleTarget{
				{Name: "extra", ClusterSelector: labelSelector(map[string]string{"extra": "true"})},
				{Name: "edge", ClusterSelector: labelSelector(map[string]string{"edge": "true"})},
			},
			clusterLabels: map[string]string{"edge": "true", "extra": "true"},
			wantNames:     []string{"extra", "edge"},
		},
		{
			name: "catch-all customization matches everything",
			customizationTargets: []fleet.BundleTarget{
				{Name: "edge", ClusterSelector: labelSelector(map[string]string{"edge": "true"})},
				{Name: "all", ClusterSelector: &metav1.LabelSelector{}},
			},
			clusterLabels: map[string]string{"edge": "true"},
			wantNames:     []string{"edge", "all"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bm, err := New(makeBundle(gitRepoTarget, tt.customizationTargets))
			require.NoError(t, err)

			got := bm.MatchAllTargetCustomizations("local", nil, tt.clusterLabels)

			var gotNames []string
			for _, bt := range got {
				gotNames = append(gotNames, bt.Name)
			}
			assert.Equal(t, tt.wantNames, gotNames)
		})
	}
}

// TestMatchAllTargetCustomizations_DoNotDeployIncluded verifies that customization targets
// with DoNotDeploy set are still returned by MatchAllTargetCustomizations — the caller
// (builder.go) is responsible for handling doNotDeploy via HasDoNotDeployTarget.
// The GitRepo target is excluded because it matches both with and without restrictions.
func TestMatchAllTargetCustomizations_DoNotDeployIncluded(t *testing.T) {
	gitRepoTarget := fleet.BundleTarget{Name: "gitrepo", ClusterSelector: &metav1.LabelSelector{}}
	customizations := []fleet.BundleTarget{
		{Name: "edge", ClusterSelector: labelSelector(map[string]string{"edge": "true"})},
		{Name: "stopper", DoNotDeploy: true, ClusterSelector: labelSelector(map[string]string{"edge": "true"})},
	}
	bm, err := New(makeBundle(gitRepoTarget, customizations))
	require.NoError(t, err)

	got := bm.MatchAllTargetCustomizations("local", nil, map[string]string{"edge": "true"})
	var names []string
	for _, g := range got {
		names = append(names, g.Name)
	}
	// Both "edge" and "stopper" match as customizations; gitrepo is excluded.
	// HasDoNotDeployTarget is what gates actual deployment.
	assert.Equal(t, []string{"edge", "stopper"}, names)
}

// TestMatchTargetCustomizations_StillFirstMatch confirms the original method is unchanged:
// with the correct ordering (customizations first, gitrepo last), it returns the first
// matching customization — stopping there and never reaching the second.
func TestMatchTargetCustomizations_StillFirstMatch(t *testing.T) {
	gitRepoTarget := fleet.BundleTarget{Name: "gitrepo", ClusterSelector: &metav1.LabelSelector{}}
	customizations := []fleet.BundleTarget{
		{Name: "edge", ClusterSelector: labelSelector(map[string]string{"edge": "true"})},
		{Name: "extra", ClusterSelector: labelSelector(map[string]string{"extra": "true"})},
	}
	bm, err := New(makeBundle(gitRepoTarget, customizations))
	require.NoError(t, err)

	got := bm.MatchTargetCustomizations("local", nil, map[string]string{"edge": "true", "extra": "true"})
	require.NotNil(t, got)
	// "edge" is the first customization in the list and matches — "extra" is never reached.
	assert.Equal(t, "edge", got.Name, "MatchTargetCustomizations returns first match without restrictions")
}

// TestCustomizationWithSameSelectorsAsGitRepoTarget verifies the fix for the bug where
// a fleet.yaml targetCustomization that shares the same (Name, ClusterSelector, etc.)
// with a GitRepo target was incorrectly classified as a GitRepo target and skipped.
//
// This test verifies that customizations are applied based on their provenance
// (fleet.yaml vs GitRepo), not just selector equality. The hybrid approach uses
// position-based detection, correctly identifying the first target as a
// customization even when selectors match.
func TestCustomizationWithSameSelectorsAsGitRepoTarget(t *testing.T) {
	// GitRepo target: deploys to clusters with env=prod
	gitRepoTarget := fleet.BundleTarget{
		Name:            "production",
		ClusterSelector: labelSelector(map[string]string{"env": "prod"}),
	}

	// fleet.yaml targetCustomization: has IDENTICAL selectors to gitRepoTarget,
	// but contains BundleDeploymentOptions (e.g., different Helm values).
	// Even though selectors match, this should still be treated as a customization.
	customization := fleet.BundleTarget{
		Name:            "production",                                    // same name
		ClusterSelector: labelSelector(map[string]string{"env": "prod"}), // same selector
		// Position-based: index 0 < numCustomizations (1), so this is a customization
	}

	// Build bundle with customization first (as bundlereader does), then GitRepo target
	bundle := &fleet.Bundle{
		Spec: fleet.BundleSpec{
			Targets: []fleet.BundleTarget{customization, gitRepoTarget},
			TargetRestrictions: []fleet.BundleTargetRestriction{
				{
					Name:            gitRepoTarget.Name,
					ClusterSelector: gitRepoTarget.ClusterSelector,
				},
			},
		},
	}

	bm, err := New(bundle)
	require.NoError(t, err)

	clusterLabels := map[string]string{"env": "prod"}

	t.Run("MatchTargetCustomizations should return the customization", func(t *testing.T) {
		// Position-based detection correctly identifies index 0 as a customization
		// even though it has identical selectors to the GitRepo target
		got := bm.MatchTargetCustomizations("cluster1", nil, clusterLabels)
		require.NotNil(t, got, "customization should match even when it has same selectors as GitRepo target")
		assert.Equal(t, "production", got.Name)
	})

	t.Run("MatchAllTargetCustomizations should include the customization", func(t *testing.T) {
		// Position-based detection correctly includes the customization at index 0
		got := bm.MatchAllTargetCustomizations("cluster1", nil, clusterLabels)
		require.Len(t, got, 1, "should return the customization even when selectors match a GitRepo target")
		assert.Equal(t, "production", got[0].Name)
	})
}

func TestDetermineIsCustomization(t *testing.T) {
	tests := []struct {
		name              string
		index             int
		numCustomizations int
		numRestrictions   int
		want              bool
	}{
		{
			name:              "position-based customization",
			index:             0,
			numCustomizations: 1,
			numRestrictions:   1,
			want:              true,
		},
		{
			name:              "position-based gitrepo target",
			index:             1,
			numCustomizations: 1,
			numRestrictions:   1,
			want:              false,
		},
		{
			name:              "no customizations (all gitrepo)",
			index:             0,
			numCustomizations: 0,
			numRestrictions:   1,
			want:              false,
		},
		{
			name:              "standalone bundle (no restrictions)",
			index:             0,
			numCustomizations: 1,
			numRestrictions:   0,
			want:              false,
		},
		{
			name:              "multiple customizations",
			index:             2,
			numCustomizations: 3,
			numRestrictions:   2,
			want:              true,
		},
		{
			name:              "boundary case (last customization)",
			index:             2,
			numCustomizations: 3,
			numRestrictions:   1,
			want:              true,
		},
		{
			name:              "boundary case (first gitrepo target)",
			index:             3,
			numCustomizations: 3,
			numRestrictions:   1,
			want:              false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineIsCustomization(tt.index, tt.numCustomizations, tt.numRestrictions)
			assert.Equal(t, tt.want, got)
		})
	}
}
