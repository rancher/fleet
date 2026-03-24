package bundlereader

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRoundTripper is a RoundTripper that is not *http.Transport, so substituting
// it for http.DefaultTransport triggers the type-assertion path in transportForAuth.
type fakeRoundTripper struct{}

func (fakeRoundTripper) RoundTrip(*http.Request) (*http.Response, error) { return nil, nil }

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

	var result http.RoundTripper
	require.NotPanics(t, func() {
		result = transportForAuth(false, nil)
	}, "transportForAuth must not panic when http.DefaultTransport is not *http.Transport")
	require.NotNil(t, result)

	// The fallback transport must carry proxy and timeout settings, not bare &http.Transport{}.
	tr, ok := result.(*http.Transport)
	require.True(t, ok, "result must be *http.Transport")
	assert.NotNil(t, tr.Proxy, "fallback transport must have Proxy set")
	assert.NotNil(t, tr.DialContext, "fallback transport must have DialContext set")
	assert.Greater(t, tr.TLSHandshakeTimeout, tr.IdleConnTimeout*0, "TLSHandshakeTimeout must be positive")

	// Also verify the custom-CA path does not panic.
	transportsCacheMutex.Lock()
	transportsCache = map[string]http.RoundTripper{}
	transportsCacheMutex.Unlock()

	require.NotPanics(t, func() {
		result = transportForAuth(false, []byte("not-a-real-cert"))
		assert.NotNil(t, result)
	}, "transportForAuth must not panic with a CA bundle when DefaultTransport is not *http.Transport")
}
