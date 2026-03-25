package bundlereader

import "testing"

func TestRedactURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain https no credentials",
			input:    "https://github.com/rancher/fleet",
			expected: "https://github.com/rancher/fleet",
		},
		{
			name:     "https with credentials",
			input:    "https://user:s3cr3t@github.com/rancher/fleet",
			expected: "https://user:xxxxx@github.com/rancher/fleet",
		},
		{
			name:     "forced git:: scheme with credentials",
			input:    "git::https://user:s3cr3t@github.com/rancher/fleet",
			expected: "git::https://user:xxxxx@github.com/rancher/fleet",
		},
		{
			name:     "forced git:: with credentials and subdir",
			input:    "git::https://user:s3cr3t@github.com/rancher/fleet//charts",
			expected: "git::https://user:xxxxx@github.com/rancher/fleet//charts",
		},
		{
			name:     "https with credentials and subdir",
			input:    "https://user:s3cr3t@github.com/rancher/fleet//charts",
			expected: "https://user:xxxxx@github.com/rancher/fleet//charts",
		},
		{
			name:     "SCP-style SSH has no password to redact",
			input:    "git@github.com:rancher/fleet.git",
			expected: "git@github.com:rancher/fleet.git",
		},
		{
			name:     "local path passes through unchanged",
			input:    "/tmp/some/path",
			expected: "/tmp/some/path",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "shorthand without credentials",
			input:    "github.com/rancher/fleet",
			expected: "github.com/rancher/fleet",
		},
		{
			name:     "sshkey query param is removed",
			input:    "https://example.com/repo?sshkey=PRIVATE_KEY_CONTENT",
			expected: "https://example.com/repo",
		},
		{
			name:     "sshkey removed alongside other query params",
			input:    "https://example.com/repo?ref=main&sshkey=PRIVATE_KEY_CONTENT",
			expected: "https://example.com/repo?ref=main",
		},
		{
			name:     "sshkey and password both redacted",
			input:    "https://user:s3cr3t@example.com/repo?sshkey=PRIVATE_KEY_CONTENT",
			expected: "https://user:xxxxx@example.com/repo",
		},
		{
			name:     "forced scheme with sshkey",
			input:    "git::https://example.com/repo?sshkey=PRIVATE_KEY_CONTENT",
			expected: "git::https://example.com/repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactURL(tt.input)
			if got != tt.expected {
				t.Errorf("redactURL(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
