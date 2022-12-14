// Package fleetyaml provides utilities for working with fleet.yaml files,
// which are the central yaml files for bundles.
package fleetyaml

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	fleetYaml         = "fleet.yaml"
	fallbackFleetYaml = "fleet.yml"
)

func FoundFleetYamlInDirectory(baseDir string) bool {
	if _, err := os.Stat(GetFleetYamlPath(baseDir, false)); err != nil {
		if _, err := os.Stat(GetFleetYamlPath(baseDir, true)); err != nil {
			return false
		}
	}
	return true
}

func GetFleetYamlPath(baseDir string, useFallbackFileExtension bool) string {
	if useFallbackFileExtension {
		return filepath.Join(baseDir, fallbackFleetYaml)
	}
	return filepath.Join(baseDir, fleetYaml)
}

func IsFleetYaml(fileName string) bool {
	if fileName == fleetYaml || fileName == fallbackFleetYaml {
		return true
	}
	return false
}

func IsFleetYamlSuffix(filePath string) bool {
	return strings.HasSuffix(filePath, "/"+fleetYaml) || strings.HasSuffix(filePath, "/"+fallbackFleetYaml)
}
