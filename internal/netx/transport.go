package netx

import (
	"net"
	"net/http"
	"time"
)

// NewTransport clones http.DefaultTransport and applies the TLS settings of c.
func NewTransport(c TLSConfig) *http.Transport {
	tr, ok := http.DefaultTransport.(*http.Transport)
	if ok {
		tr = tr.Clone()
	} else {
		// Another component has replaced the global default transport: keep the
		// stdlib defaults, notably proxy settings from the environment.
		tr = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
	}
	tr.TLSClientConfig = c.TLSClientConfig()
	return tr
}
