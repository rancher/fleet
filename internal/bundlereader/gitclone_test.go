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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
