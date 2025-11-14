package helmdeployer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v4/pkg/release"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
)

func TestReleaserToV1Release(t *testing.T) {
	tests := []struct {
		name        string
		input       release.Releaser
		wantErr     bool
		expectedNil bool
	}{
		{
			name: "converts pointer to v1.Release successfully",
			input: &releasev1.Release{
				Name:    "test-release",
				Version: 1,
			},
			wantErr:     false,
			expectedNil: false,
		},
		{
			name: "converts value v1.Release successfully",
			input: releasev1.Release{
				Name:    "test-release-value",
				Version: 2,
			},
			wantErr:     false,
			expectedNil: false,
		},
		{
			name:        "handles nil input",
			input:       nil,
			wantErr:     false,
			expectedNil: true,
		},
		{
			name:    "returns error for unsupported type",
			input:   "not-a-release",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := releaserToV1Release(tt.input)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, result)
				assert.Contains(t, err.Error(), "unsupported release type")
				return
			}

			require.NoError(t, err)

			if tt.expectedNil {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				// Verify the conversion preserved the data
				switch r := tt.input.(type) {
				case *releasev1.Release:
					assert.Equal(t, r.Name, result.Name)
					assert.Equal(t, r.Version, result.Version)
				case releasev1.Release:
					assert.Equal(t, r.Name, result.Name)
					assert.Equal(t, r.Version, result.Version)
				}
			}
		})
	}
}

func TestReleaseListToV1List(t *testing.T) {
	tests := []struct {
		name    string
		input   []release.Releaser
		wantErr bool
		wantLen int
	}{
		{
			name: "converts list successfully",
			input: []release.Releaser{
				&releasev1.Release{Name: "release-1", Version: 1},
				&releasev1.Release{Name: "release-2", Version: 2},
				&releasev1.Release{Name: "release-3", Version: 3},
			},
			wantErr: false,
			wantLen: 3,
		},
		{
			name:    "handles empty list",
			input:   []release.Releaser{},
			wantErr: false,
			wantLen: 0,
		},
		{
			name: "handles nil in list",
			input: []release.Releaser{
				&releasev1.Release{Name: "release-1", Version: 1},
				nil,
				&releasev1.Release{Name: "release-2", Version: 2},
			},
			wantErr: false,
			wantLen: 3,
		},
		{
			name: "returns error for unsupported type in list",
			input: []release.Releaser{
				&releasev1.Release{Name: "release-1", Version: 1},
				"invalid-type",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := releaseListToV1List(tt.input)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, result)
				return
			}

			require.NoError(t, err)
			assert.Len(t, result, tt.wantLen)

			// Verify data preservation for non-nil releases
			for i, r := range tt.input {
				if r != nil {
					switch rel := r.(type) {
					case *releasev1.Release:
						assert.Equal(t, rel.Name, result[i].Name)
						assert.Equal(t, rel.Version, result[i].Version)
					}
				}
			}
		})
	}
}

// Note: Tests for listReleases, getReleaseHistory, and getLastRelease are covered
// by the existing tests in delete.go and other helmdeployer tests, as they require
// full release initialization with charts, metadata, and info objects.
