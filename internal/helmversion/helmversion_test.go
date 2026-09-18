package helmversion_test

import (
	"testing"

	"github.com/Masterminds/semver/v3"

	"github.com/rancher/fleet/internal/helmversion"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		version  string
		expected string
	}{
		{version: "", expected: ""},
		{version: "1.2.3", expected: "1.2.3"},
		{version: "v1.2.3", expected: "v1.2.3"},
		{version: "1.2", expected: "1.2"},
		{version: "109.0.1+up1.0.2", expected: "109.0.1+up1.0.2"},
		{version: "109.0.1_up1.0.2", expected: "109.0.1+up1.0.2"},
		{version: "v109.0.1_up1.0.2", expected: "v109.0.1+up1.0.2"},
		{version: "1.2.3-rc1_build01", expected: "1.2.3-rc1+build01"},
		// Constraints and anything else which is not a version are left alone.
		{version: ">= 1.0.0", expected: ">= 1.0.0"},
		{version: "1.1.*", expected: "1.1.*"},
		{version: "latest", expected: "latest"},
		{version: "my_tag", expected: "my_tag"},
	}

	for _, c := range cases {
		t.Run(c.version, func(t *testing.T) {
			if got := helmversion.Normalize(c.version); got != c.expected {
				t.Errorf("expected %q, got %q", c.expected, got)
			}
		})
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		version     string
		expectedErr bool
	}{
		{version: "1.2.3"},
		{version: "1.2.3+up1.0.0"},
		{version: "1.2.3-rc1"},
		{version: "1.2.3-rc1+up1.0.0"},
		// Some Helm repositories publish their versions with a "v" prefix, and
		// Fleet accepts those
		{version: "v1.2.3"},
		{version: "v1.2.3+up1.0.0"},
		// Versions semver only accepts loosely cannot be deployed, so they are no
		// versions as far as resolution is concerned.
		{version: "1.2", expectedErr: true},
		{version: "1", expectedErr: true},
		{version: "1.02.3", expectedErr: true},
		{version: "v1.2", expectedErr: true},
		{version: "", expectedErr: true},
		{version: "latest", expectedErr: true},
		// The tag spelling of build metadata is not a version; FromTag converts it.
		{version: "1.2.3_up1.0.0", expectedErr: true},
	}

	for _, c := range cases {
		t.Run(c.version, func(t *testing.T) {
			sv, err := helmversion.Parse(c.version)
			if c.expectedErr {
				if err == nil {
					t.Fatalf("expected an error, got version %v", sv)
				}

				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if sv == nil {
				t.Fatal("expected a version, got nil")
			}
		})
	}
}

func TestParseExact(t *testing.T) {
	cases := []struct {
		version          string
		expectedExact    bool
		expectedMetadata string
	}{
		{version: "1.2.3", expectedExact: true},
		{version: "1.2.3+up1.0.0", expectedExact: true, expectedMetadata: "up1.0.0"},
		{version: "1.2.3_up1.0.0", expectedExact: true, expectedMetadata: "up1.0.0"},
		{version: "1.2.3-rc1", expectedExact: true},
		{version: "", expectedExact: false},
		{version: "1.2", expectedExact: false},
		{version: "v1.2.3", expectedExact: false},
		{version: ">= 1.0.0", expectedExact: false},
		{version: "1.2.*", expectedExact: false},
	}

	for _, c := range cases {
		t.Run(c.version, func(t *testing.T) {
			sv, ok := helmversion.ParseExact(c.version)
			if ok != c.expectedExact {
				t.Fatalf("expected exact to be %v, got %v", c.expectedExact, ok)
			}

			if !ok {
				return
			}

			if sv.Metadata() != c.expectedMetadata {
				t.Errorf("expected metadata %q, got %q", c.expectedMetadata, sv.Metadata())
			}
		})
	}
}

func TestEqual(t *testing.T) {
	cases := []struct {
		a, b     string
		expected bool
	}{
		{a: "1.1.2", b: "1.1.2", expected: true},
		{a: "1.1.2+build01", b: "1.1.2+build01", expected: true},
		// semver.Version.Equal ignores build metadata; these must not.
		{a: "1.1.2", b: "1.1.2+build01", expected: false},
		{a: "1.1.2+build01", b: "1.1.2+build02", expected: false},
		{a: "1.1.2", b: "1.1.3", expected: false},
		{a: "1.1.2-rc1+build01", b: "1.1.2+build01", expected: false},
	}

	for _, c := range cases {
		t.Run(c.a+" vs "+c.b, func(t *testing.T) {
			if got := helmversion.Equal(mustParse(t, c.a), mustParse(t, c.b)); got != c.expected {
				t.Errorf("expected %v, got %v", c.expected, got)
			}
		})
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b     string
		expected int
	}{
		{a: "1.1.2", b: "1.1.2", expected: 0},
		{a: "1.1.2+build01", b: "1.1.2+build01", expected: 0},
		{a: "1.1.3", b: "1.1.2+build99", expected: 1},
		{a: "1.1.2-rc1", b: "1.1.2", expected: -1},
		// A version with build metadata takes precedence over the same version
		// without it, as that is the build a registry publishes.
		{a: "1.1.2", b: "1.1.2+build01", expected: -1},
		{a: "1.1.2+build01", b: "1.1.2", expected: 1},
		// Otherwise the highest metadata wins.
		{a: "1.1.2+build02", b: "1.1.2+build01", expected: 1},
		{a: "109.0.1+up1.0.10", b: "109.0.1+up1.0.2", expected: 1},
		// Digits inside a single identifier are part of an alphanumeric string, so
		// they compare lexically, exactly as semver has pre-release identifiers do.
		{a: "109.0.1+up2", b: "109.0.1+up10", expected: 1},
		{a: "1.1.2+up1.0", b: "1.1.2+up1.0.1", expected: -1},
		{a: "1.1.2+1", b: "1.1.2+up1", expected: -1},
		// A numeric identifier is compared as a number whatever its width, rather
		// than being parsed into an integer which it may not fit.
		{a: "1.1.2+100000000000000000000", b: "1.1.2+99999999999999999999", expected: 1},
		{a: "1.1.2+99999999999999999999", b: "1.1.2+99999999999999999998", expected: 1},
		{a: "1.1.2+18446744073709551616", b: "1.1.2+18446744073709551615", expected: 1},
		{a: "1.1.2+99999999999999999999", b: "1.1.2+2", expected: 1},
		// A number too wide to parse is still a number, so it stays below any
		// alphanumeric identifier.
		{a: "1.1.2+99999999999999999999", b: "1.1.2+abc", expected: -1},
		// Leading zeroes carry no value.
		{a: "1.1.2+007", b: "1.1.2+7", expected: 0},
		{a: "1.1.2+007", b: "1.1.2+8", expected: -1},
	}

	for _, c := range cases {
		t.Run(c.a+" vs "+c.b, func(t *testing.T) {
			got := helmversion.Compare(mustParse(t, c.a), mustParse(t, c.b))
			if got != c.expected {
				t.Errorf("expected %d, got %d", c.expected, got)
			}

			// The ordering must be antisymmetric, or resolution would depend on the
			// order in which a registry happens to list its tags.
			if reverse := helmversion.Compare(mustParse(t, c.b), mustParse(t, c.a)); reverse != -got {
				t.Errorf("expected reversed comparison to be %d, got %d", -got, reverse)
			}
		})
	}
}

func mustParse(t *testing.T, v string) *semver.Version {
	t.Helper()

	sv, err := semver.NewVersion(v)
	if err != nil {
		t.Fatalf("failed to parse version %q: %v", v, err)
	}

	return sv
}
