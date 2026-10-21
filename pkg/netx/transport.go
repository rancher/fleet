package netx

import (
	"net"
	"net/http"
	"time"
)

// NewTransport clones http.DefaultTransport and applies the TLS settings of c.
func NewTransport(c Config) *http.Transport {
	tr := baseTransport()
	tr.TLSClientConfig = c.TLSConfig()
	return tr
}

func baseTransport() *http.Transport {
	if tr, ok := http.DefaultTransport.(*http.Transport); ok {
		return tr.Clone()
	}
	// Another component has replaced the global default transport: keep the
	// stdlib defaults, notably proxy settings from the environment.
	return &http.Transport{
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
