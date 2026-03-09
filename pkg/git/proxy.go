package git

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"golang.org/x/net/http/httpproxy"
	"golang.org/x/net/proxy"
)

func init() {
	proxy.RegisterDialerType("http", newHTTPConnectDialer)
	proxy.RegisterDialerType("https", newHTTPConnectDialer)
}

// ProxyOptsFromEnvironment reads the standard HTTP_PROXY / HTTPS_PROXY /
// NO_PROXY environment variables and returns a transport.ProxyOptions value
// ready to be embedded in go-git CloneOptions or ListOptions.
//
// Why this is necessary: go-git's HTTP transport uses http.DefaultTransport,
// which already honors HTTP_PROXY / HTTPS_PROXY natively. However, go-git's
// SSH transport only routes through a proxy when ProxyOptions.URL is non-empty
// — it never reads the proxy env vars itself. Without wiring ProxyOptions the
// registered httpConnectDialer would never be invoked for SSH repos.
//
// Proxy selection and NO_PROXY matching are delegated to
// golang.org/x/net/http/httpproxy, which follows the same rules as net/http.
// SSH and scp-style repos are looked up as https:// because SSH traffic is
// tunnelled through a CONNECT proxy the same way HTTPS is. Both HTTP_PROXY
// and HTTPS_PROXY work; HTTPS_PROXY is checked first for SSH URLs.
func ProxyOptsFromEnvironment(repoURL string) transport.ProxyOptions {
	if repoURL == "" {
		return transport.ProxyOptions{}
	}

	proxyFn := httpproxy.FromEnvironment().ProxyFunc()

	// HTTP/HTTPS URLs are passed directly. SSH and scp-style URLs are looked
	// up as https:// so that HTTPS_PROXY is preferred, with HTTP_PROXY as
	// fallback (SSH traffic tunnels through CONNECT like HTTPS does).
	var proxyURL *url.URL
	if u, err := url.Parse(repoURL); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		// proxyFn error means the configured proxy URL is malformed; treat as
		// no proxy (proxyURL stays nil and we return empty opts below).
		proxyURL, _ = proxyFn(u)
	} else {
		// SSH/scp-style: we have no scheme to pass directly, so synthesize
		// lookup URLs. Try https first (HTTPS_PROXY) then http (HTTP_PROXY).
		// NO_PROXY is checked per-host by proxyFn regardless of scheme.
		// Only fall back to HTTP_PROXY when the https lookup returns (nil, nil)
		// — i.e. HTTPS_PROXY is simply not set. An error means HTTPS_PROXY is
		// set but malformed, so we stop rather than silently use a different proxy.
		host := hostFromRepoURL(repoURL)
		var err error
		proxyURL, err = proxyFn(&url.URL{Scheme: "https", Host: host})
		if proxyURL == nil && err == nil {
			proxyURL, _ = proxyFn(&url.URL{Scheme: "http", Host: host})
		}
	}

	if proxyURL == nil {
		return transport.ProxyOptions{}
	}

	opts := transport.ProxyOptions{URL: proxyURL.String()}
	if proxyURL.User != nil {
		opts.Username = proxyURL.User.Username()
		opts.Password, _ = proxyURL.User.Password()
	}
	return opts
}

// hostFromRepoURL extracts the hostname from a git repository URL.
// It handles ssh:// and scp-style (git@host:path) URLs.
func hostFromRepoURL(repoURL string) string {
	if u, err := url.Parse(repoURL); err == nil && u.Host != "" {
		return u.Hostname()
	}
	// scp-style: git@github.com:org/repo.git
	if _, after, ok := strings.Cut(repoURL, "@"); ok {
		rest := after
		if before, _, ok := strings.Cut(rest, ":"); ok {
			return before
		}
	}
	return repoURL
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
	return &httpConnectDialer{
		proxyURL: proxyURL,
		forward:  fwd,
	}, nil
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
	req.Header.Set("User-Agent", "git/fleet")

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

	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
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
