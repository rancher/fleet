//go:build !windows

package fleetyaml

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBundleYaml(t *testing.T) {
	a := assert.New(t)
	for _, path := range []string{"/foo", "foo", "/foo/", "foo/", "../foo/bar"} {

		// Test both the primary extension and the fallback extension.
		for _, fullPath := range []string{GetFleetYamlPath(path, false), GetFleetYamlPath(path, true)} {
			a.True(IsFleetYaml(filepath.Base(fullPath)))
			a.True(IsFleetYamlSuffix(fullPath))
		}
	}

	// Test expected failure payloads.
	for _, fullPath := range []string{"fleet.yaaaaaaaaaml", "", ".", "weakmonkey.yaml", "../fleet.yaaaaml"} {
		a.False(IsFleetYaml(filepath.Base(fullPath)))
		a.False(IsFleetYamlSuffix(fullPath))
	}
}
