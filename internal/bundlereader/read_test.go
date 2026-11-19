package bundlereader

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// TestBundleFromDir_TargetCustomizationModePropagate verifies that the
// targetCustomizationMode field from fleet.yaml is correctly unmarshalled into
// bundle.Spec.TargetCustomizationMode via the embedded BundleSpec.
func TestBundleFromDir_TargetCustomizationModePropagate(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name     string
		yaml     string
		wantMode fleet.TargetCustomizationMode
	}{
		{
			name:     "AllMatches is propagated to BundleSpec",
			yaml:     `targetCustomizationMode: AllMatches`,
			wantMode: fleet.TargetCustomizationModeAllMatches,
		},
		{
			name:     "FirstMatch is propagated to BundleSpec",
			yaml:     `targetCustomizationMode: FirstMatch`,
			wantMode: fleet.TargetCustomizationModeFirstMatch,
		},
		{
			name:     "omitted mode propagates as empty string (controller uses default)",
			yaml:     `namespace: test`,
			wantMode: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bundle, _, err := bundleFromDir(context.Background(), "test", dir, []byte(tt.yaml), nil)
			require.NoError(t, err)
			assert.Equal(t, tt.wantMode, bundle.Spec.TargetCustomizationMode)
		})
	}
}

func TestBundleFromDir_RepoLabel(t *testing.T) {
	tests := []struct {
		name       string
		repoLabel  string
		source     string
		wantLabels map[string]string
	}{
		{
			name:       "source cannot replace the supplied repository identity",
			repoLabel:  "application-dev",
			source:     "  fleet.cattle.io/repo-name: application-qa\n",
			wantLabels: map[string]string{fleet.RepoLabel: "application-dev"},
		},
		{
			name:       "source cannot clear the supplied repository identity",
			repoLabel:  "application-dev",
			source:     "  fleet.cattle.io/repo-name: \"\"\n",
			wantLabels: map[string]string{fleet.RepoLabel: "application-dev"},
		},
		{
			name:       "supplied repository identity is retained without a source label",
			repoLabel:  "application-dev",
			wantLabels: map[string]string{fleet.RepoLabel: "application-dev"},
		},
		{
			name:       "source label is retained without a supplied repository identity",
			source:     "  fleet.cattle.io/repo-name: application-qa\n",
			wantLabels: map[string]string{fleet.RepoLabel: "application-qa"},
		},
		{
			name:       "repository identity is not inferred from the bundle name",
			wantLabels: map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &Options{Labels: map[string]string{"example.com/component": "configs"}}
			if tt.repoLabel != "" {
				opts.Labels[fleet.RepoLabel] = tt.repoLabel
			}
			data := []byte("labels:\n  example.com/component: application\n" + tt.source)
			bundle, _, err := bundleFromDir(t.Context(), "application-dev-application", t.TempDir(), data, opts)
			require.NoError(t, err)
			// Ordinary source labels still take precedence over supplied labels.
			tt.wantLabels["example.com/component"] = "application"
			assert.Equal(t, tt.wantLabels, bundle.Labels)
		})
	}
}
