package giturls_test

import (
	"testing"

	giturls "github.com/rancher/fleet/pkg/git-urls"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "ssh",
			url:  "ssh://example.com/foo/bar",
			want: "ssh://example.com/foo/bar",
		},
		{
			name: "git",
			url:  "git://example.com/foo/bar",
			want: "git://example.com/foo/bar",
		},
		{
			name: "git+ssh",
			url:  "git+ssh://example.com/foo/bar",
			want: "git+ssh://example.com/foo/bar",
		},
		{
			name: "http",
			url:  "http://example.com/foo/bar",
			want: "http://example.com/foo/bar",
		},
		{
			name: "https",
			url:  "https://example.com/foo/bar",

			want: "https://example.com/foo/bar",
		},
		{
			name: "scp",
			url:  "git@example.com:foo/bar",
			want: "ssh://git@example.com/foo/bar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := giturls.Parse(tt.url)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if u.String() != tt.want {
				t.Errorf("got %q, want %q", u.String(), tt.want)
			}
		})
	}
}
