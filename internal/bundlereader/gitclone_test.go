package bundlereader

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fleetgit "github.com/rancher/fleet/pkg/git"
)

// newSelfSignedTLSServer returns an HTTPS test server with a freshly generated
// self-signed certificate and the PEM-encoded certificate.
// Every call returns a unique cert so that different servers can be told apart.
func newSelfSignedTLSServer(t *testing.T) (*httptest.Server, []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{tlsCert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	return srv, certPEM
}

// TestGitDownloadCABundle verifies TLS certificate handling for gitDownload:
//
//   - No CA bundle: TLS fails because the server's self-signed cert is not in
//     the system pool.
//   - Correct CA bundle: TLS handshake succeeds; only a git-protocol error
//     follows (the httptest server does not speak git HTTP protocol).
//   - Valid but mismatched CA bundle: providing a valid cert from a different
//     server still fails TLS because the target's cert is trusted by neither the
//     mismatched cert's CA nor the system pool.
//   - Invalid CA bundle: rejected immediately before any TLS attempt.
//
// Each server uses a freshly generated self-signed certificate so that the
// test is independent of any system CA store.
func TestGitDownloadCABundle(t *testing.T) {
	t.Parallel()

	// srv is the target clone server.
	srv, srvCertPEM := newSelfSignedTLSServer(t)

	// otherSrv is a second TLS server whose cert we use as a "wrong" CA bundle.
	// Because each call to newSelfSignedTLSServer generates a unique key pair,
	// srvCertPEM != otherCertPEM.
	_, otherCertPEM := newSelfSignedTLSServer(t)

	t.Run("no CA bundle fails with TLS error", func(t *testing.T) {
		t.Parallel()
		dst := t.TempDir()
		err := gitDownload(context.Background(), dst, srv.URL, Auth{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "certificate")
	})

	t.Run("correct CA bundle gets past TLS", func(t *testing.T) {
		t.Parallel()
		dst := t.TempDir()
		err := gitDownload(context.Background(), dst, srv.URL, Auth{CABundle: srvCertPEM})
		require.Error(t, err)
		// TLS succeeded; expect a git-protocol error, not a TLS error.
		assert.NotContains(t, err.Error(), "certificate")
	})

	t.Run("valid but mismatched CA bundle fails with TLS error", func(t *testing.T) {
		t.Parallel()
		dst := t.TempDir()
		// otherCertPEM is a valid PEM cert from a different server.
		// Neither it nor the system pool covers srv's cert, so TLS must fail.
		err := gitDownload(context.Background(), dst, srv.URL, Auth{CABundle: otherCertPEM})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "certificate")
	})

	t.Run("invalid CA bundle is rejected immediately", func(t *testing.T) {
		t.Parallel()
		dst := t.TempDir()
		err := gitDownload(context.Background(), dst, srv.URL, Auth{CABundle: []byte("not-a-cert")})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no valid PEM certificates")
	})
}

// TestGitDownloadProxyCABundle verifies that PROXY_CA_BUNDLE is merged into
// the effective CA bundle used for TLS verification in gitDownload.
//
//   - PROXY_CA_BUNDLE alone (no auth.CABundle): TLS succeeds when the env var
//     contains the server's cert, confirming the merge happens even without an
//     explicit CA bundle in the Auth struct.
//   - PROXY_CA_BUNDLE merged with auth.CABundle: both certs are trusted.
//   - Empty PROXY_CA_BUNDLE: falls back to auth.CABundle only.
//
// Not parallel: the test mutates the process-global PROXY_CA_BUNDLE env var.
func TestGitDownloadProxyCABundle(t *testing.T) {
	srv, srvCertPEM := newSelfSignedTLSServer(t)
	otherSrv, otherCertPEM := newSelfSignedTLSServer(t)

	t.Run("PROXY_CA_BUNDLE alone trusts the server", func(t *testing.T) {
		t.Setenv(fleetgit.ProxyCABundleEnvVar, string(srvCertPEM))
		dst := t.TempDir()
		err := gitDownload(context.Background(), dst, srv.URL, Auth{})
		require.Error(t, err)
		// TLS succeeded; expect a git-protocol error, not a certificate error.
		assert.NotContains(t, err.Error(), "certificate")
	})

	t.Run("PROXY_CA_BUNDLE is merged with auth.CABundle", func(t *testing.T) {
		// auth.CABundle covers srv; PROXY_CA_BUNDLE covers otherSrv.
		t.Setenv(fleetgit.ProxyCABundleEnvVar, string(otherCertPEM))

		// auth.CABundle server: trusted via auth.CABundle (PROXY_CA_BUNDLE not needed).
		dst := t.TempDir()
		err := gitDownload(context.Background(), dst, srv.URL, Auth{CABundle: srvCertPEM})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "certificate", "auth.CABundle server should get past TLS")

		// PROXY_CA_BUNDLE server: trusted via the merged env var cert.
		dst = t.TempDir()
		err = gitDownload(context.Background(), dst, otherSrv.URL, Auth{CABundle: srvCertPEM})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "certificate", "PROXY_CA_BUNDLE server should get past TLS via merge")
	})

	t.Run("empty PROXY_CA_BUNDLE uses auth.CABundle only", func(t *testing.T) {
		t.Setenv(fleetgit.ProxyCABundleEnvVar, "")
		dst := t.TempDir()
		err := gitDownload(context.Background(), dst, srv.URL, Auth{CABundle: srvCertPEM})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "certificate")
	})

	t.Run("wrong PROXY_CA_BUNDLE without auth.CABundle fails with TLS error", func(t *testing.T) {
		// otherCertPEM does not cover srv, and there is no auth.CABundle fallback,
		// so TLS must fail with a certificate error.
		t.Setenv(fleetgit.ProxyCABundleEnvVar, string(otherCertPEM))
		dst := t.TempDir()
		err := gitDownload(context.Background(), dst, srv.URL, Auth{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "certificate")
	})

	t.Run("no PROXY_CA_BUNDLE and no auth.CABundle fails with TLS error", func(t *testing.T) {
		t.Setenv(fleetgit.ProxyCABundleEnvVar, "")
		dst := t.TempDir()
		err := gitDownload(context.Background(), dst, srv.URL, Auth{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "certificate")
	})
}

// generateEd25519PEM returns a PEM-encoded Ed25519 private key for use in tests.
func generateEd25519PEM(t *testing.T) []byte {
	t.Helper()

	// Use ECDSA since crypto/ed25519 and go-git's SSH key parsing both work,
	// but PEM encoding of Ed25519 requires the x/crypto package. ECDSA P-256
	// is simpler to produce inline.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

// TestSetGitAuth verifies that setGitAuth respects URL scheme when choosing
// which auth type to configure.
func TestSetGitAuth(t *testing.T) {
	t.Parallel()

	keyPEM := generateEd25519PEM(t)

	t.Run("sshKey in URL for HTTPS URL returns error", func(t *testing.T) {
		t.Parallel()
		u, _ := url.Parse("https://example.com/repo.git")
		opts := &gogit.CloneOptions{}
		err := setGitAuth(opts, u, keyPEM, Auth{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "?sshkey= is not supported")
		assert.Nil(t, opts.Auth)
	})

	t.Run("Auth.SSHPrivateKey alongside HTTPS URL is ignored", func(t *testing.T) {
		t.Parallel()
		u, _ := url.Parse("https://example.com/repo.git")
		opts := &gogit.CloneOptions{}
		err := setGitAuth(opts, u, nil, Auth{SSHPrivateKey: keyPEM})
		require.NoError(t, err)
		// No auth configured: callers that need basic auth supply URL credentials.
		assert.Nil(t, opts.Auth)
	})

	t.Run("sshKey in URL for SSH URL configures SSH auth", func(t *testing.T) {
		t.Parallel()
		u, _ := url.Parse("ssh://git@example.com/repo.git")
		opts := &gogit.CloneOptions{}
		err := setGitAuth(opts, u, keyPEM, Auth{})
		require.NoError(t, err)
		assert.NotNil(t, opts.Auth)
	})

	t.Run("Auth.SSHPrivateKey for SSH URL configures SSH auth", func(t *testing.T) {
		t.Parallel()
		u, _ := url.Parse("ssh://git@example.com/repo.git")
		opts := &gogit.CloneOptions{}
		err := setGitAuth(opts, u, nil, Auth{SSHPrivateKey: keyPEM})
		require.NoError(t, err)
		assert.NotNil(t, opts.Auth)
	})

	t.Run("basic auth from URL userinfo for HTTPS URL", func(t *testing.T) {
		t.Parallel()
		u, _ := url.Parse("https://user:pass@example.com/repo.git")
		opts := &gogit.CloneOptions{}
		err := setGitAuth(opts, u, nil, Auth{})
		require.NoError(t, err)
		assert.NotNil(t, opts.Auth)
	})
}
