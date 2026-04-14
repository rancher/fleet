package git

import (
	"strings"
	"testing"
)

// TestNewRemote_ProxyCABundle_MergedIntoLister verifies that when
// PROXY_CA_BUNDLE is set, its PEM is appended to the CA bundle stored in
// GoGitRemoteLister so go-git's HTTP transport trusts the proxy CA.
func TestNewRemote_ProxyCABundle_MergedIntoLister(t *testing.T) {
	const proxyPEM = "-----BEGIN CERTIFICATE-----\nproxycert\n-----END CERTIFICATE-----"
	t.Setenv(ProxyCABundleEnvVar, proxyPEM)

	r, err := NewRemote("http://example.com/repo.git", &options{})
	if err != nil {
		t.Fatalf("NewRemote: %v", err)
	}

	lister, ok := r.Lister.(*GoGitRemoteLister)
	if !ok {
		t.Fatalf("expected *GoGitRemoteLister, got %T", r.Lister)
	}
	if !strings.Contains(string(lister.CABundle), proxyPEM) {
		t.Errorf("GoGitRemoteLister.CABundle does not contain proxy PEM\ngot: %q", string(lister.CABundle))
	}
}

// TestNewRemote_ProxyCABundle_MergedWithExistingCABundle verifies that when
// both opts.CABundle and PROXY_CA_BUNDLE are set, both PEMs appear in the
// lister's CABundle (file CA first, then proxy CA).
func TestNewRemote_ProxyCABundle_MergedWithExistingCABundle(t *testing.T) {
	const (
		filePEM  = "-----BEGIN CERTIFICATE-----\nfilecert\n-----END CERTIFICATE-----"
		proxyPEM = "-----BEGIN CERTIFICATE-----\nproxycert\n-----END CERTIFICATE-----"
	)
	t.Setenv(ProxyCABundleEnvVar, proxyPEM)

	r, err := NewRemote("http://example.com/repo.git", &options{
		CABundle: []byte(filePEM),
	})
	if err != nil {
		t.Fatalf("NewRemote: %v", err)
	}

	lister, ok := r.Lister.(*GoGitRemoteLister)
	if !ok {
		t.Fatalf("expected *GoGitRemoteLister, got %T", r.Lister)
	}
	got := string(lister.CABundle)
	for _, want := range []string{filePEM, proxyPEM} {
		if !strings.Contains(got, want) {
			t.Errorf("GoGitRemoteLister.CABundle missing %q\ngot: %q", want, got)
		}
	}
	if idx := strings.Index(got, filePEM); idx > strings.Index(got, proxyPEM) {
		t.Error("expected file PEM to appear before proxy PEM in CABundle")
	}
}

// TestNewRemote_NoProxyCABundle_UsesOnlyOpts verifies that without
// PROXY_CA_BUNDLE the lister's CABundle equals opts.CABundle exactly.
func TestNewRemote_NoProxyCABundle_UsesOnlyOpts(t *testing.T) {
	const filePEM = "-----BEGIN CERTIFICATE-----\nfilecert\n-----END CERTIFICATE-----"
	t.Setenv(ProxyCABundleEnvVar, "")

	r, err := NewRemote("http://example.com/repo.git", &options{
		CABundle: []byte(filePEM),
	})
	if err != nil {
		t.Fatalf("NewRemote: %v", err)
	}

	lister, ok := r.Lister.(*GoGitRemoteLister)
	if !ok {
		t.Fatalf("expected *GoGitRemoteLister, got %T", r.Lister)
	}
	if got := string(lister.CABundle); got != filePEM {
		t.Errorf("expected CABundle %q, got %q", filePEM, got)
	}
}
