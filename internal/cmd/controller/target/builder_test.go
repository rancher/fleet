package target

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestPreprocessNamespaceMetadata(t *testing.T) {
	tests := []struct {
		name                string
		bundleYaml          string
		expectedLabels      map[string]string
		expectedAnnotations map[string]string
		expectedErr         string
	}{
		{
			name: "renders templates without helm options",
			bundleYaml: `namespaceLabels:
  env: "${ .ClusterLabels.testLabel }"
  team: alpha
namespaceAnnotations:
  field.cattle.io/projectId: "${ .ClusterName }:p-${ .ClusterValues.someKey }"
`,
			expectedLabels: map[string]string{"env": "test-label-value", "team": "alpha"},
			expectedAnnotations: map[string]string{
				"field.cattle.io/projectId": "test-cluster:p-someValue",
			},
		},
		{
			name: "keeps templates when preprocessing is disabled",
			bundleYaml: `helm:
  disablePreprocess: true
namespaceLabels:
  env: "${ .ClusterLabels.testLabel }"
`,
			expectedLabels: map[string]string{"env": "${ .ClusterLabels.testLabel }"},
		},
		{
			name: "fails on missing cluster label",
			bundleYaml: `namespaceLabels:
  env: "${ .ClusterLabels.missing }"
`,
			expectedErr: `namespaceLabels: failed to render template for key "env"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cluster, opts, err := getClusterAndBundle(tc.bundleYaml)
			require.NoError(t, err)

			original := maps.Clone(opts.NamespaceLabels)
			bundleLabels := opts.NamespaceLabels

			err = preprocessNamespaceMetadata(opts, cluster)
			if tc.expectedErr != "" {
				require.ErrorContains(t, err, tc.expectedErr)
				return
			}
			require.NoError(t, err)

			assert.Equal(t, tc.expectedLabels, opts.NamespaceLabels)
			assert.Equal(t, tc.expectedAnnotations, opts.NamespaceAnnotations)
			// the bundle's map is shared by all targets and must not be modified
			assert.Equal(t, original, bundleLabels)
		})
	}
}

func TestPreprocessNamespaceMetadata_NoMetadata(t *testing.T) {
	opts := &v1alpha1.BundleDeploymentOptions{}

	require.NoError(t, preprocessNamespaceMetadata(opts, &v1alpha1.Cluster{}))
	assert.Nil(t, opts.NamespaceLabels)
	assert.Nil(t, opts.NamespaceAnnotations)
}
