package giturls_test

import (
	"net/url"
	"strings"
	"testing"

	giturls "github.com/rancher/fleet/pkg/git-urls"
)

func TestParse(t *testing.T) {
	cases := map[string]struct {
		input       string
		expectedURL *url.URL
		expectedErr string
	}{
		"HTTP": {
			input: "http://foo.bar/baz",
			expectedURL: &url.URL{
				Scheme: "http",
				Host:   "foo.bar",
				Path:   "/baz",
			},
		},
		"HTTPS": {
			input: "https://foo.bar/baz",
			expectedURL: &url.URL{
				Scheme: "https",
				Host:   "foo.bar",
				Path:   "/baz",
			},
		},
		"HTTP with credentials": {
			input: "https://fleet-ci:foo@git-service.fleet-local.svc.cluster.local:8080/repo",
			expectedURL: &url.URL{
				Scheme: "https",
				User:   url.UserPassword("fleet-ci", "foo"),
				Host:   "git-service.fleet-local.svc.cluster.local:8080",
				Path:   "/repo",
			},
		},
		"SSH": {
			input: "ssh://foo.bar/baz",
			expectedURL: &url.URL{
				Scheme: "ssh",
				Host:   "foo.bar",
				Path:   "/baz",
			},
		},
		"git": {
			input: "git://foo.bar/baz",
			expectedURL: &url.URL{
				Scheme: "git",
				Host:   "foo.bar",
				Path:   "/baz",
			},
		},
		"git+ssh": {
			input: "git+ssh://foo.bar/baz",
			expectedURL: &url.URL{
				Scheme: "git+ssh",
				Host:   "foo.bar",
				Path:   "/baz",
			},
		},
		"ssh with user": {
			input: "git@github.com:rancher/fleet",
			expectedURL: &url.URL{
				Scheme: "ssh",
				User:   url.User("git"),
				Host:   "github.com",
				Path:   "rancher/fleet",
			},
		},
		"ftp": { // deprecated as per https://github.com/git/git/blob/master/Documentation/urls.txt#L10-L11
			input:       "ftp://foo.bar/baz",
			expectedURL: nil,
			expectedErr: "scheme URL",
		},
		"too long": {
			input:       "git@github.com/" + strings.Repeat("foo/", 512),
			expectedURL: nil,
			expectedErr: "too long",
		},
		"invalid": {
			input:       "foo",
			expectedURL: nil,
			expectedErr: "failed to parse",
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			u, err := giturls.Parse(c.input)

			if (err != nil && c.expectedErr == "") || (err == nil && c.expectedErr != "") {
				t.Fatalf("mismatch in errors: expected %v, got %v", c.expectedErr, err)
			}

			if c.expectedErr != "" && !strings.Contains(err.Error(), c.expectedErr) {
				t.Fatalf("expected error message to contain %q, got %v", c.expectedErr, err)
			}

			if (u == nil && c.expectedURL != nil) || (u != nil && c.expectedURL == nil) {
				t.Fatalf("expected URL %v, got %v", c.expectedURL, u)
			}

			if u == c.expectedURL {
				return // for instance if both are nil
			}

			if u.Scheme != c.expectedURL.Scheme {
				t.Fatalf("expected URL scheme \n%#v\ngot\n%#v", c.expectedURL.Scheme, u.Scheme)
			}

			if u.User.String() != c.expectedURL.User.String() {
				t.Fatalf("expected URL User \n%#v\ngot\n%#v", c.expectedURL.User, u.User)
			}

			if u.Host != c.expectedURL.Host {
				t.Fatalf("expected URL Host \n%#v\ngot\n%#v", c.expectedURL.Host, u.Host)
			}

			if u.Path != c.expectedURL.Path {
				t.Fatalf("expected URL Path \n%#v\ngot\n%#v", c.expectedURL.Path, u.Path)
			}

			if u.RawPath != c.expectedURL.RawPath {
				t.Fatalf("expected URL RawPath \n%#v\ngot\n%#v", c.expectedURL.RawPath, u.RawPath)
			}

			if u.RawQuery != c.expectedURL.RawQuery {
				t.Fatalf("expected URL RawQuery \n%#v\ngot\n%#v", c.expectedURL.RawQuery, u.RawQuery)
			}

			if u.Fragment != c.expectedURL.Fragment {
				t.Fatalf("expected URL Fragment \n%#v\ngot\n%#v", c.expectedURL.Fragment, u.Fragment)
			}

			if u.RawFragment != c.expectedURL.RawFragment {
				t.Fatalf("expected URL RawFragment \n%#v\ngot\n%#v", c.expectedURL.RawFragment, u.RawFragment)
			}
		})
	}
}
