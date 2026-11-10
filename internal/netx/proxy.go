package netx

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"

	"golang.org/x/net/proxy"

	"github.com/rancher/fleet/internal/httputils"
)

// ProxyCABundleEnvVar is the name of the environment variable that holds the
// PEM-encoded CA certificate used to trust HTTPS proxies with custom or
// self-signed certificates. The value is appended to the system cert pool so
// that well-known public CA certificates remain trusted.
const ProxyCABundleEnvVar = "PROXY_CA_BUNDLE"

// isTLSRecord returns true when the first byte of data received from the proxy
// looks like a TLS record (content types 20–23 cover change_cipher_spec, alert,
// handshake, and application_data). This lets us emit a friendlier error when
// a plain-HTTP CONNECT request is sent to a TLS-only proxy port.
func isTLSRecord(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	// TLS record content-type values: 20=change_cipher_spec, 21=alert,
	// 22=handshake, 23=application_data.
	return b[0] >= 20 && b[0] <= 23
}

func init() {
	proxy.RegisterDialerType("http", newHTTPConnectDialer)
	proxy.RegisterDialerType("https", newHTTPConnectDialer)
}

// httpConnectDialer tunnels a TCP connection through an HTTP proxy using the
// CONNECT method, as understood by Squid and most other HTTP forward proxies.
// It implements both proxy.Dialer and proxy.ContextDialer so that go-git's SSH
// transport, which calls proxy.FromURL and then asserts proxy.ContextDialer,
// works transparently with HTTP_PROXY / HTTPS_PROXY environment variables.
type httpConnectDialer struct {
	proxyURL  *url.URL
	forward   proxy.ContextDialer
	tlsConfig *tls.Config // nil means use system defaults; only consulted when proxyURL.Scheme == "https"
}

// newHTTPConnectDialer is the factory registered with proxy.RegisterDialerType.
// The forward argument is typed as proxy.Dialer by the library's API; we
// require it to also implement proxy.ContextDialer. In practice the only
// caller is go-git, which always passes proxy.Direct — a type that implements
// both interfaces.
func newHTTPConnectDialer(proxyURL *url.URL, forward proxy.Dialer) (proxy.Dialer, error) {
	if forward == nil {
		forward = proxy.Direct
	}
	fwd, ok := forward.(proxy.ContextDialer)
	if !ok {
		return nil, fmt.Errorf("http connect proxy: forward dialer %T does not implement proxy.ContextDialer", forward)
	}
	d := &httpConnectDialer{
		proxyURL: proxyURL,
		forward:  fwd,
	}
	if proxyURL.Scheme == "https" {
		if caPEM, ok := os.LookupEnv(ProxyCABundleEnvVar); ok && caPEM != "" {
			pool, err := x509.SystemCertPool()
			if err != nil {
				pool = x509.NewCertPool()
			}
			if ok := pool.AppendCertsFromPEM([]byte(caPEM)); !ok {
				return nil, fmt.Errorf("http connect proxy: %s does not contain any valid PEM-encoded certificates", ProxyCABundleEnvVar)
			}
			d.tlsConfig = &tls.Config{RootCAs: pool}
		}
	}
	return d, nil
}

// Dial implements proxy.Dialer.
func (d *httpConnectDialer) Dial(network, addr string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, addr)
}

// DialContext implements proxy.ContextDialer.
// It opens a connection to the proxy and issues an HTTP CONNECT request to
// tunnel to addr. On success it returns the raw conn ready for use by the
// SSH handshake.
func (d *httpConnectDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	proxyAddr := d.proxyURL.Host
	if d.proxyURL.Port() == "" {
		switch d.proxyURL.Scheme {
		case "https":
			proxyAddr = net.JoinHostPort(d.proxyURL.Hostname(), "443")
		default:
			proxyAddr = net.JoinHostPort(d.proxyURL.Hostname(), "3128")
		}
	}

	conn, err := d.forward.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("http connect proxy: dial proxy %s: %w", proxyAddr, err)
	}

	// For https:// proxy URLs, upgrade the plain TCP connection to TLS before
	// sending the CONNECT request. This protects the Proxy-Authorization header
	// and the CONNECT line itself from eavesdropping on the path to the proxy.
	if d.proxyURL.Scheme == "https" {
		tlsCfg := d.tlsConfig
		if tlsCfg == nil {
			tlsCfg = &tls.Config{ServerName: d.proxyURL.Hostname()}
		} else {
			tlsCfg = tlsCfg.Clone()
			if tlsCfg.ServerName == "" {
				tlsCfg.ServerName = d.proxyURL.Hostname()
			}
		}
		tlsConn := tls.Client(conn, tlsCfg)
		// HandshakeContext honors ctx cancellation/deadline internally.
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			conn.Close()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, fmt.Errorf("http connect proxy: TLS handshake with proxy %s: %w", proxyAddr, ctxErr)
			}
			return nil, fmt.Errorf("http connect proxy: TLS handshake with proxy %s: %w", proxyAddr, err)
		}
		conn = tlsConn
	}

	// Neither req.Write(conn) nor http.ReadResponse observe ctx on their own,
	// so close the connection from a goroutine if ctx is cancelled or times
	// out. Closing the conn unblocks any in-progress Write/Read immediately.
	// The goroutine is started after the TLS block so it captures the final
	// value of conn (plain or TLS) without a data race.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()

	// Send the CONNECT request over the connection.
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: make(http.Header),
	}
	req.Header.Set("User-Agent", httputils.UserAgent())

	if user := d.proxyURL.User; user != nil {
		username := user.Username()
		password, _ := user.Password()
		creds := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		req.Header.Set("Proxy-Authorization", "Basic "+creds)
	}

	if err := req.Write(conn); err != nil {
		conn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("http connect proxy: write CONNECT request: %w", ctxErr)
		}
		return nil, fmt.Errorf("http connect proxy: write CONNECT request: %w", err)
	}

	br := bufio.NewReader(conn)

	// Peek at the first byte before attempting to parse an HTTP response.
	// If the proxy is TLS-only and we sent a plain HTTP CONNECT request, it
	// will reply with a TLS record (content type 20–23). Detecting this early
	// produces a clear, actionable error instead of the confusing "malformed
	// HTTP response \x15\x03..." message that http.ReadResponse would emit
	// after consuming and failing to parse the TLS bytes.
	if first, err := br.Peek(1); err == nil && isTLSRecord(first) {
		conn.Close()
		return nil, fmt.Errorf(
			"http connect proxy: proxy %s responded with a TLS record instead of an HTTP response — "+
				"the proxy URL scheme may need to be \"https://\" and its certificate must be trusted",
			proxyAddr,
		)
	}

	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("http connect proxy: read CONNECT response: %w", ctxErr)
		}
		return nil, fmt.Errorf("http connect proxy: read CONNECT response: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("http connect proxy: CONNECT to %s via %s failed with status %d %s",
			addr, proxyAddr, resp.StatusCode, resp.Status)
	}

	return conn, nil
}
