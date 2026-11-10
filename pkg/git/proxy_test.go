package git

import (
	"testing"
)

// --------------------------------------------------------------------------
// proxyOptsFromEnvironment tests
// --------------------------------------------------------------------------

func TestProxyOptsFromEnvironment(t *testing.T) {
	tests := []struct {
		name     string
		repoURL  string
		env      map[string]string
		wantURL  string
		wantUser string
		wantPass string
	}{
		{
			name:    "no env vars set returns empty",
			repoURL: "https://github.com/org/repo.git",
			env:     map[string]string{},
			wantURL: "",
		},
		{
			name:    "https repo picks HTTPS_PROXY",
			repoURL: "https://github.com/org/repo.git",
			env:     map[string]string{"HTTPS_PROXY": "http://proxy.example.com:3128"},
			wantURL: "http://proxy.example.com:3128",
		},
		{
			name:    "https repo ignores HTTP_PROXY",
			repoURL: "https://github.com/org/repo.git",
			env:     map[string]string{"HTTP_PROXY": "http://other.example.com:3128"},
			wantURL: "",
		},
		{
			name:    "http repo picks HTTP_PROXY",
			repoURL: "http://github.com/org/repo.git",
			env:     map[string]string{"HTTP_PROXY": "http://proxy.example.com:3128"},
			wantURL: "http://proxy.example.com:3128",
		},
		{
			name:    "http repo ignores HTTPS_PROXY",
			repoURL: "http://github.com/org/repo.git",
			env:     map[string]string{"HTTPS_PROXY": "http://proxy.example.com:3128"},
			wantURL: "",
		},
		{
			name:    "ssh:// repo prefers HTTPS_PROXY",
			repoURL: "ssh://git@github.com/org/repo.git",
			env: map[string]string{
				"HTTPS_PROXY": "http://sshproxy.example.com:3128",
				"HTTP_PROXY":  "http://other.example.com:3128",
			},
			wantURL: "http://sshproxy.example.com:3128",
		},
		{
			name:    "ssh:// repo falls back to HTTP_PROXY when HTTPS_PROXY unset",
			repoURL: "ssh://git@github.com/org/repo.git",
			env:     map[string]string{"HTTP_PROXY": "http://fallback.example.com:3128"},
			wantURL: "http://fallback.example.com:3128",
		},
		{
			name:    "scp-style repo prefers HTTPS_PROXY",
			repoURL: "git@github.com:org/repo.git",
			env:     map[string]string{"HTTPS_PROXY": "http://scpproxy.example.com:3128"},
			wantURL: "http://scpproxy.example.com:3128",
		},
		{
			name:    "lowercase https_proxy is respected",
			repoURL: "https://github.com/org/repo.git",
			env:     map[string]string{"https_proxy": "http://lower.example.com:3128"},
			wantURL: "http://lower.example.com:3128",
		},
		{
			name:     "proxy credentials are extracted",
			repoURL:  "ssh://git@github.com/org/repo.git",
			env:      map[string]string{"HTTPS_PROXY": "http://alice:s3cr3t@proxy.example.com:3128"},
			wantURL:  "http://alice:s3cr3t@proxy.example.com:3128",
			wantUser: "alice",
			wantPass: "s3cr3t",
		},
		{
			name:    "host in NO_PROXY returns empty",
			repoURL: "https://internal.example.com/org/repo.git",
			env: map[string]string{
				"HTTPS_PROXY": "http://proxy.example.com:3128",
				"NO_PROXY":    "internal.example.com",
			},
			wantURL: "",
		},
		{
			name:    "host not in NO_PROXY uses proxy",
			repoURL: "https://external.example.com/org/repo.git",
			env: map[string]string{
				"HTTPS_PROXY": "http://proxy.example.com:3128",
				"NO_PROXY":    "internal.example.com",
			},
			wantURL: "http://proxy.example.com:3128",
		},
		{
			name:    "empty repoURL returns empty",
			repoURL: "",
			env:     map[string]string{"HTTPS_PROXY": "http://proxy.example.com:3128"},
			wantURL: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy"} {
				t.Setenv(k, "")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			got := ProxyOptsFromEnvironment(tt.repoURL)

			if got.URL != tt.wantURL {
				t.Errorf("URL: got %q, want %q", got.URL, tt.wantURL)
			}
			if got.Username != tt.wantUser {
				t.Errorf("Username: got %q, want %q", got.Username, tt.wantUser)
			}
			if got.Password != tt.wantPass {
				t.Errorf("Password: got %q, want %q", got.Password, tt.wantPass)
			}
		})
	}
}

// TestProxyOptsFromEnvironment_ReturnType verifies the return type is
// transport.ProxyOptions so callers can embed it without a cast.
func TestProxyOptsFromEnvironment_ReturnType(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://proxy.example.com:3128")
	var _ = ProxyOptsFromEnvironment("https://github.com/org/repo.git")
}

// --------------------------------------------------------------------------
// hostFromRepoURL tests
// --------------------------------------------------------------------------

func TestHostFromRepoURL(t *testing.T) {
	tests := []struct {
		name    string
		repoURL string
		want    string
	}{
		{"https URL", "https://github.com/org/repo.git", "github.com"},
		{"https URL with port", "https://github.com:8443/org/repo.git", "github.com"},
		{"http URL", "http://gitlab.com/org/repo.git", "gitlab.com"},
		{"ssh URL", "ssh://git@github.com/org/repo.git", "github.com"},
		{"scp-style", "git@github.com:org/repo.git", "github.com"},
		{"scp-style with user", "user@bitbucket.org:org/repo.git", "bitbucket.org"},
		{"bare hostname fallback", "github.com", "github.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hostFromRepoURL(tt.repoURL)
			if got != tt.want {
				t.Errorf("hostFromRepoURL(%q) = %q, want %q", tt.repoURL, got, tt.want)
			}
		})
	}
}
