package bundlereader

import (
	"encoding/base64"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitForcedScheme(t *testing.T) {
	tests := []struct {
		name         string
		src          string
		wantScheme   string
		wantStripped string
	}{
		{"git:: prefix", "git::https://example.com/repo.git", "git", "https://example.com/repo.git"},
		{"ssh:: prefix", "ssh::git@example.com:org/repo", "ssh", "git@example.com:org/repo"},
		{"http:: prefix", "http::http://example.com/file.tar.gz", "http", "http://example.com/file.tar.gz"},
		{"no prefix", "https://example.com/repo.git", "", "https://example.com/repo.git"},
		{"local path", "/some/local/path", "", "/some/local/path"},
		{"unknown prefix is ignored", "foo::https://example.com", "", "foo::https://example.com"},
		{"no double colons", "https://example.com/repo", "", "https://example.com/repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme, stripped := splitForcedScheme(tt.src)
			assert.Equal(t, tt.wantScheme, scheme)
			assert.Equal(t, tt.wantStripped, stripped)
		})
	}
}

func TestSplitSubdir(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantBase   string
		wantSubdir string
	}{
		{
			name:       "no subdir",
			src:        "https://host/repo.git",
			wantBase:   "https://host/repo.git",
			wantSubdir: "",
		},
		{
			name:       "subdir without query string",
			src:        "https://host/repo.git//path/to/dir",
			wantBase:   "https://host/repo.git",
			wantSubdir: "path/to/dir",
		},
		{
			name:       "subdir with query string on subdir part",
			src:        "https://host/repo.git//subdir?ref=main",
			wantBase:   "https://host/repo.git?ref=main",
			wantSubdir: "subdir",
		},
		{
			name:       "no subdir but has query string",
			src:        "https://host/repo?ref=main",
			wantBase:   "https://host/repo?ref=main",
			wantSubdir: "",
		},
		{
			name:       "git:: prefix stripped, subdir present",
			src:        "https://host/repo//charts",
			wantBase:   "https://host/repo",
			wantSubdir: "charts",
		},
		{
			name:       "scheme separator does not count as subdir marker",
			src:        "ssh://git@host/repo",
			wantBase:   "ssh://git@host/repo",
			wantSubdir: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, subdir := splitSubdir(tt.src)
			assert.Equal(t, tt.wantBase, base, "base URL")
			assert.Equal(t, tt.wantSubdir, subdir, "subdir")
		})
	}
}

func TestParseSource_ForcedGetters(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantScheme string
		wantURL    string
	}{
		{
			name:       "git::https URL resolves to git scheme",
			src:        "git::https://example.com/repo.git",
			wantScheme: "git",
			wantURL:    "https://example.com/repo.git",
		},
		{
			name:       "git::ssh URL resolves to git scheme",
			src:        "git::ssh://git@example.com/repo.git",
			wantScheme: "git",
			wantURL:    "ssh://git@example.com/repo.git",
		},
		{
			name:       "ssh::ssh URL resolves to git scheme",
			src:        "ssh::ssh://git@example.com/repo.git",
			wantScheme: "git",
			wantURL:    "ssh://git@example.com/repo.git",
		},
		{
			name:       "http:: prefix resolves to http scheme",
			src:        "http::https://example.com/file.tar.gz",
			wantScheme: "http",
			wantURL:    "https://example.com/file.tar.gz",
		},
		{
			name:       "git:: with SCP-style SSH normalizes to ssh:// URL",
			src:        "git::git@github.com:org/repo",
			wantScheme: "git",
			wantURL:    "ssh://git@github.com/org/repo",
		},
		{
			name:       "ssh:: with non-git user normalizes to ssh:// URL",
			src:        "ssh::alice@example.com:org/repo",
			wantScheme: "git",
			wantURL:    "ssh://alice@example.com/org/repo",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSource(tt.src, "")
			require.NoError(t, err)
			assert.Equal(t, tt.wantScheme, got.scheme)
			assert.Equal(t, tt.wantURL, got.rawURL)
		})
	}
}

func TestParseSource_SchemeURLs(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantScheme string
	}{
		{"ssh:// scheme is git", "ssh://git@example.com/repo.git", "git"},
		{"git:// scheme is git", "git://example.com/repo.git", "git"},
		{"https:// scheme without forced is http", "https://example.com/file.tar.gz", "http"},
		{"http:// scheme is http", "http://example.com/file.tar.gz", "http"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSource(tt.src, "")
			require.NoError(t, err)
			assert.Equal(t, tt.wantScheme, got.scheme)
		})
	}
}

func TestParseSource_Shorthands(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantScheme string
		wantOK     bool
	}{
		{
			name:       "github.com shorthand is git",
			src:        "github.com/foo/bar",
			wantScheme: "git",
			wantOK:     true,
		},
		{
			name:       "gitlab.com shorthand is git",
			src:        "gitlab.com/foo/bar",
			wantScheme: "git",
			wantOK:     true,
		},
		{
			name:       "bitbucket.org shorthand is git",
			src:        "bitbucket.org/foo/bar",
			wantScheme: "git",
			wantOK:     true,
		},
		{
			name:       "SCP SSH shorthand is git",
			src:        "git@example.com:org/repo",
			wantScheme: "git",
			wantOK:     true,
		},
		{
			name:       "SCP SSH shorthand with non-git user is git",
			src:        "alice@example.com:org/repo",
			wantScheme: "git",
			wantOK:     true,
		},
		{
			name:       "unknown host falls back to local",
			src:        "example.com/foo/bar",
			wantScheme: "local",
			wantOK:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSource(tt.src, "")
			require.NoError(t, err)
			assert.Equal(t, tt.wantScheme, got.scheme)
			if tt.wantOK {
				assert.NotEmpty(t, got.rawURL)
			}
		})
	}
}

func TestParseSource_SubdirExtraction(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantSubdir string
		wantURL    string
	}{
		{
			name:       "git::https with //subdir",
			src:        "git::https://host/repo.git//charts",
			wantSubdir: "charts",
			wantURL:    "https://host/repo.git",
		},
		{
			name:       "git::https with //subdir and ?ref=",
			src:        "git::https://host/repo.git//charts?ref=main",
			wantSubdir: "charts",
			wantURL:    "https://host/repo.git?ref=main",
		},
		{
			name:       "github.com shorthand with subpath becomes subdir",
			src:        "github.com/foo/bar/deploy",
			wantSubdir: "deploy",
		},
		{
			name:       "no subdir",
			src:        "git::https://host/repo.git",
			wantSubdir: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSource(tt.src, "")
			require.NoError(t, err)
			assert.Equal(t, tt.wantSubdir, got.subDir, "subdir")
			if tt.wantURL != "" {
				assert.Equal(t, tt.wantURL, got.rawURL, "rawURL")
			}
		})
	}
}

func TestParseSource_EmptySource(t *testing.T) {
	got, err := parseSource("", "/my/pwd")
	require.NoError(t, err)
	assert.Equal(t, "local", got.scheme)
	assert.Equal(t, "/my/pwd", got.rawURL)
}

func TestParseSource_LocalRelativePaths(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		pwd     string
		wantURL string
	}{
		{"dot resolves to pwd", ".", "/my/pwd", "/my/pwd"},
		{"relative path joined with pwd", "charts", "/my/pwd", "/my/pwd/charts"},
		{"absolute path unchanged", "/abs/path", "/my/pwd", "/abs/path"},
		{"empty pwd leaves relative as-is", "charts", "", "charts"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSource(tt.src, tt.pwd)
			require.NoError(t, err)
			assert.Equal(t, "local", got.scheme)
			assert.Equal(t, tt.wantURL, got.rawURL)
		})
	}
}

func TestSafeJoinSubDir(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		sub     string
		want    string
		wantErr bool
	}{
		{"normal subdir", "/base", "charts", "/base/charts", false},
		{"nested subdir", "/base", "a/b/c", "/base/a/b/c", false},
		{"dot returns base", "/base", ".", "/base", false},
		{"absolute path rejected", "/base", "/etc/passwd", "", true},
		{"single traversal rejected", "/base", "../etc", "", true},
		{"deep traversal rejected", "/base", "charts/../../etc", "", true},
		{"traversal in nested path", "/base", "a/../../../etc", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := safeJoinSubDir(tt.base, tt.sub)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSafeJoin(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		input   string
		want    string
		wantErr bool
	}{
		{"plain file", "/base", "file.txt", "/base/file.txt", false},
		{"nested path", "/base", "sub/dir/file.txt", "/base/sub/dir/file.txt", false},
		// Traversal attempts are neutralised by prepending "/" before filepath.Clean,
		// placing the result safely inside base rather than rejecting it.
		{"path traversal sanitised", "/base", "../etc/passwd", "/base/etc/passwd", false},
		{"deep traversal sanitised", "/base", "sub/../../etc/passwd", "/base/etc/passwd", false},
		// An absolute path inside an archive is rewritten safely within base.
		{"absolute treated as relative", "/base", "/etc/passwd", "/base/etc/passwd", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := safeJoin(tt.base, tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestExtractQueryParams(t *testing.T) {
	keyBytes := []byte("fake-ssh-key")
	keyB64 := base64.RawURLEncoding.EncodeToString(keyBytes)

	tests := []struct {
		name         string
		rawURL       string
		wantRef      string
		wantKey      []byte
		wantDepth    int
		wantErr      bool
		wantRawQuery string // if non-empty, u.RawQuery must equal this after extraction
	}{
		{
			name:    "no params",
			rawURL:  "https://host/repo.git",
			wantRef: "",
		},
		{
			name:    "ref only",
			rawURL:  "https://host/repo.git?ref=main",
			wantRef: "main",
		},
		{
			name:      "depth only",
			rawURL:    "https://host/repo.git?depth=5",
			wantDepth: 5,
		},
		{
			name:    "sshkey only",
			rawURL:  "https://host/repo.git?sshkey=" + keyB64,
			wantKey: keyBytes,
		},
		{
			name:      "all params",
			rawURL:    "https://host/repo.git?ref=v1.0&depth=3&sshkey=" + keyB64,
			wantRef:   "v1.0",
			wantDepth: 3,
			wantKey:   keyBytes,
		},
		{
			name:    "invalid sshkey base64 returns error",
			rawURL:  "https://host/repo.git?sshkey=!!!invalid!!!",
			wantErr: true,
		},
		{
			// Non-Fleet params must survive with their original encoding and
			// ordering; url.Values.Encode() would reorder and re-encode them.
			name:         "non-Fleet params preserve original encoding and ordering",
			rawURL:       "https://host/repo.git?token=ab+cd%2Fef&ref=main&extra=1%202",
			wantRef:      "main",
			wantRawQuery: "token=ab+cd%2Fef&extra=1%202",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.rawURL)
			require.NoError(t, err)

			ref, key, depth, err := extractQueryParams(u)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantRef, ref, "ref")
			assert.Equal(t, tt.wantKey, key, "sshKey")
			assert.Equal(t, tt.wantDepth, depth, "depth")
			// Fleet params must be stripped from the URL.
			assert.Empty(t, u.Query().Get("ref"))
			assert.Empty(t, u.Query().Get("sshkey"))
			assert.Empty(t, u.Query().Get("depth"))
			if tt.wantRawQuery != "" {
				assert.Equal(t, tt.wantRawQuery, u.RawQuery, "rawQuery")
			}
		})
	}
}
