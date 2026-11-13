package bundlereader

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rancher/fleet/internal/netx"
)

// fakeRoundTripper is a RoundTripper that is not *http.Transport, so substituting
// it for http.DefaultTransport triggers the type-assertion path in transportForAuth.
type fakeRoundTripper struct{}

func (fakeRoundTripper) RoundTrip(*http.Request) (*http.Response, error) { return nil, nil }

// TestTransportForAuth_ProxyCABundle verifies that when PROXY_CA_BUNDLE is set,
// transportForAuth merges the proxy CA into the TLS cert pool.  We confirm
// this indirectly by checking that two calls with different env var values
// produce different transports (different cache keys) — which only happens if
// the env var content is included in the bundle passed to transportHash.
//
// Not parallel: the test mutates the process-global env and transport cache.
func TestTransportForAuth_ProxyCABundle(t *testing.T) {
	// Obtain a valid PEM certificate from a TLS test server.
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ts.Close()
	caDER := ts.TLS.Certificates[0].Certificate[0]
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))

	// Clear the cache once so we start from a clean state.
	transportsCacheMutex.Lock()
	transportsCache = map[string]http.RoundTripper{}
	transportsCacheMutex.Unlock()

	t.Setenv(netx.ProxyCABundleEnvVar, "")
	rtWithout, err := transportForAuth(netx.TLSConfig{})
	if err != nil {
		t.Fatal(err)
	}

	// Change the env var to a valid cert — the hash must change, producing a
	// new cache entry.  If PROXY_CA_BUNDLE were not included in the hash, both
	// calls would return the same cached instance and the assertion below would
	// catch the regression.
	t.Setenv(netx.ProxyCABundleEnvVar, caPEM)
	rtWith, err := transportForAuth(netx.TLSConfig{})
	if err != nil {
		t.Fatal(err)
	}

	if rtWithout == rtWith {
		t.Error("expected different transports when PROXY_CA_BUNDLE changes, got the same instance")
	}

	// The transport produced with a CA bundle must still be a *http.Transport.
	if _, ok := rtWith.(*http.Transport); !ok {
		t.Errorf("expected *http.Transport, got %T", rtWith)
	}
}

// TestTransportForAuthNonDefaultTransport verifies that transportForAuth does not
// panic when http.DefaultTransport has been replaced by a value that is not
// *http.Transport, and that the resulting transport has proxy and timeout defaults.
//
// Not parallel: the test mutates the process-global http.DefaultTransport.
func TestTransportForAuthNonDefaultTransport(t *testing.T) {
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })

	http.DefaultTransport = fakeRoundTripper{}

	// The transport cache is package-level; clear it so a fresh entry is built.
	transportsCacheMutex.Lock()
	transportsCache = map[string]http.RoundTripper{}
	transportsCacheMutex.Unlock()

	var (
		result http.RoundTripper
		err    error
	)
	require.NotPanics(t, func() {
		result, err = transportForAuth(netx.TLSConfig{})
	}, "transportForAuth must not panic when http.DefaultTransport is not *http.Transport")
	require.NoError(t, err)
	require.NotNil(t, result)

	// The fallback transport must carry proxy and timeout settings, not bare &http.Transport{}.
	tr, ok := result.(*http.Transport)
	require.True(t, ok, "result must be *http.Transport")
	assert.NotNil(t, tr.Proxy, "fallback transport must have Proxy set")
	assert.NotNil(t, tr.DialContext, "fallback transport must have DialContext set")
	assert.Greater(t, tr.TLSHandshakeTimeout, tr.IdleConnTimeout*0, "TLSHandshakeTimeout must be positive")

	// Also verify the custom-CA path does not panic.
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ts.Close()
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ts.TLS.Certificates[0].Certificate[0]})

	transportsCacheMutex.Lock()
	transportsCache = map[string]http.RoundTripper{}
	transportsCacheMutex.Unlock()

	require.NotPanics(t, func() {
		result, err = transportForAuth(netx.TLSConfig{CABundle: caPEM})
		require.NoError(t, err)
		assert.NotNil(t, result)
	}, "transportForAuth must not panic with a CA bundle when DefaultTransport is not *http.Transport")

	// An invalid CA bundle is rejected instead of being silently ignored.
	_, err = transportForAuth(netx.TLSConfig{CABundle: []byte("not-a-real-cert")})
	require.Error(t, err)
}
