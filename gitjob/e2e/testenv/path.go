package testenv

import "path"

var (
	root = "../.."
)

// SetRoot set the root path for the other relative paths, e.g. AssetPath.
// Usually set to point to the repositories root.
func SetRoot(dir string) {
	root = dir
}

// Root returns the relative path to the repositories root
func Root() string {
	return root
}

// AssetPath returns the path to an asset
func AssetPath(p ...string) string {
	parts := append([]string{root, "e2e", "assets"}, p...)
	return path.Join(parts...)
}
