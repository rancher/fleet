package matcher

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClusterMatcher_DisplayNameMatching(t *testing.T) {
	tests := []struct {
		name              string
		clusterName       string
		testClusterName   string
		testClusterLabels map[string]string
		expectedMatch     bool
		description       string
	}{
		{
			name:              "match by cluster resource name",
			clusterName:       "c-m-12345",
			testClusterName:   "c-m-12345",
			testClusterLabels: map[string]string{},
			expectedMatch:     true,
			description:       "Should match when clusterName equals the cluster resource name",
		},
		{
			name:            "match by display name label",
			clusterName:     "my-cluster",
			testClusterName: "c-m-12345",
			testClusterLabels: map[string]string{
				ClusterDisplayNameLabel: "my-cluster",
			},
			expectedMatch: true,
			description:   "Should match when clusterName equals the display name label",
		},
		{
			name:            "no match when neither name matches",
			clusterName:     "my-cluster",
			testClusterName: "c-m-12345",
			testClusterLabels: map[string]string{
				ClusterDisplayNameLabel: "other-cluster",
			},
			expectedMatch: false,
			description:   "Should not match when clusterName matches neither resource name nor display name",
		},
		{
			name:            "match resource name even with different display name",
			clusterName:     "c-m-12345",
			testClusterName: "c-m-12345",
			testClusterLabels: map[string]string{
				ClusterDisplayNameLabel: "my-cluster",
			},
			expectedMatch: true,
			description:   "Should match by resource name even if display name is different",
		},
		{
			name:              "no match when display name label is missing",
			clusterName:       "my-cluster",
			testClusterName:   "c-m-12345",
			testClusterLabels: map[string]string{},
			expectedMatch:     false,
			description:       "Should not match when display name label is missing and resource name doesn't match",
		},
		{
			name:            "match display name for imported cluster",
			clusterName:     "specific-name-1",
			testClusterName: "c-12345",
			testClusterLabels: map[string]string{
				ClusterDisplayNameLabel: "specific-name-1",
			},
			expectedMatch: true,
			description:   "Should match imported cluster by display name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher, err := NewClusterMatcher(tt.clusterName, "", nil, nil)
			require.NoError(t, err)

			result := matcher.Match(tt.testClusterName, "", nil, tt.testClusterLabels)
			assert.Equal(t, tt.expectedMatch, result, tt.description)
		})
	}
}

func TestNewClusterMatcher_EmptyClusterName(t *testing.T) {
	// When clusterName is empty, no criteria should be added
	matcher, err := NewClusterMatcher("", "", nil, nil)
	require.NoError(t, err)

	// With no criteria, Match should return false
	result := matcher.Match("any-cluster", "", nil, map[string]string{})
	assert.False(t, result, "Should return false when no criteria are defined")
}

func TestNewClusterMatcher_ClusterGroup(t *testing.T) {
	matcher, err := NewClusterMatcher("", "my-group", nil, nil)
	require.NoError(t, err)

	// Should match when clusterGroup matches
	result := matcher.Match("any-cluster", "my-group", nil, map[string]string{})
	assert.True(t, result, "Should match when clusterGroup matches")

	// Should not match when clusterGroup doesn't match
	result = matcher.Match("any-cluster", "other-group", nil, map[string]string{})
	assert.False(t, result, "Should not match when clusterGroup doesn't match")
}

func TestNewClusterMatcher_CombinedCriteria(t *testing.T) {
	// Test with both clusterName and clusterGroup - all criteria must match
	matcher, err := NewClusterMatcher("my-cluster", "my-group", nil, nil)
	require.NoError(t, err)

	// Both match - should succeed
	result := matcher.Match("c-12345", "my-group", nil, map[string]string{
		ClusterDisplayNameLabel: "my-cluster",
	})
	assert.True(t, result, "Should match when both clusterName (via display name) and clusterGroup match")

	// Only clusterName matches - should fail
	result = matcher.Match("c-12345", "other-group", nil, map[string]string{
		ClusterDisplayNameLabel: "my-cluster",
	})
	assert.False(t, result, "Should not match when only clusterName matches but clusterGroup doesn't")

	// Only clusterGroup matches - should fail
	result = matcher.Match("c-12345", "my-group", nil, map[string]string{
		ClusterDisplayNameLabel: "other-cluster",
	})
	assert.False(t, result, "Should not match when only clusterGroup matches but clusterName doesn't")
}
