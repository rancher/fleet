package imagescan

import (
	"testing"
)

func TestPathResolutionStaysInsideBase(t *testing.T) {
	const base = "/tmp/repo"
	tests := []struct {
		name    string
		relPath string
		escapes bool
	}{
		{"plain subdirectory", "manifests", false},
		{"nested subdirectory", "charts/my-chart", false},
		{"current directory", ".", false},
		{"root slash", "/", false},
		{"parent traversal", "../etc", true},
		{"deep traversal", "../../etc", true},
		{"nested then traversal", "sub/../../etc", true},
		{"leading-slash traversal", "/../etc", true},
		{"absolute path stays inside", "/etc/passwd", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pathEscapesBase(base, tt.relPath); got != tt.escapes {
				t.Errorf("pathEscapesBase(%q, %q) = %v, want %v", base, tt.relPath, got, tt.escapes)
			}
		})
	}
}
