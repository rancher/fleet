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
