package helmupdater_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rancher/fleet/internal/helmupdater"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fsNodeSimple struct {
	name     string
	children []fsNodeSimple
	isDir    bool
}

// createDirStruct generates and populates a directory structure which root is node, placing it at basePath.
func createDirStruct(t *testing.T, basePath string, node fsNodeSimple) {
	t.Helper()
	path := filepath.Join(basePath, node.name)

	if !node.isDir {
		_, err := os.Create(path)
		require.NoError(t, err)
		return
	}

	err := os.Mkdir(path, 0777)
	require.NoError(t, err)

	for _, c := range node.children {
		createDirStruct(t, path, c)
	}
}

func TestChartYAMLExists(t *testing.T) {
	cases := []struct {
		name               string
		testDir            string
		directoryStructure fsNodeSimple
		expectedResult     bool
	}{
		{
			name:    "simple case having Chart yaml file in the root",
			testDir: "simpleok",
			directoryStructure: fsNodeSimple{
				name: "Chart.yaml",
			},
			expectedResult: true,
		},
		{
			name:    "Chart.yml file instead of Chart.yaml",
			testDir: "extensionnotyaml",
			directoryStructure: fsNodeSimple{
				name: "Chart.yml",
			},
			expectedResult: false,
		},
		{
			name:    "simple case not having Chart yaml file in the root",
			testDir: "simplenotfound",
			directoryStructure: fsNodeSimple{
				name: "whatever.foo",
			},
			expectedResult: false,
		},
		{
			name:    "Chart.yaml is located in a subdirectory",
			testDir: "subdir",
			directoryStructure: fsNodeSimple{
				isDir: true,
				children: []fsNodeSimple{
					{
						name: "Chart.yaml",
					},
				},
				name: "whatever",
			},
			expectedResult: false,
		},
	}

	base, err := os.MkdirTemp("", "test-fleet")
	require.NoError(t, err)

	defer os.RemoveAll(base)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			testDirPath := filepath.Join(base, c.testDir)
			err := os.Mkdir(testDirPath, 0777)
			require.NoError(t, err)
			defer os.RemoveAll(testDirPath)

			createDirStruct(t, testDirPath, c.directoryStructure)
			found := helmupdater.ChartYAMLExists(testDirPath)

			assert.Equal(t, c.expectedResult, found)
		})
	}
}
