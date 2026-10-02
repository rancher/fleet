// Package httputils holds helpers shared by the HTTP clients Fleet uses to
// reach remote content: Helm repositories, OCI registries and git servers.
package httputils

import (
	"net/http"

	"github.com/rancher/fleet/pkg/version"
)

// UserAgent is the value Fleet sends in the User-Agent header of its outbound
// requests, so that registry and git server operators can tell Fleet's traffic
// apart from other clients'.
func UserAgent() string {
	return "fleet/" + version.Version
}

// UserAgentTransport stamps UserAgent on the requests passing through it before
// handing them over to the transport it wraps. It leaves a request which already
// carries a User-Agent alone, so that a caller spelling out its own keeps it.
//
// Setting it as the Transport of a client covers every request that client
// sends, including the ones libraries such as Helm and ORAS build out of
// Fleet's reach.
type UserAgentTransport struct {
	Next http.RoundTripper
}

func (t UserAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	next := t.Next
	if next == nil {
		next = http.DefaultTransport
	}

	if req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		if req.Header == nil {
			req.Header = make(http.Header)
		}
		req.Header.Set("User-Agent", UserAgent())
	}

	return next.RoundTrip(req)
}

// CloseIdleConnections forwards to the wrapped transport, so that
// http.Client.CloseIdleConnections keeps working on a client using it.
func (t UserAgentTransport) CloseIdleConnections() {
	next := t.Next
	if next == nil {
		next = http.DefaultTransport
	}

	if c, ok := next.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}
