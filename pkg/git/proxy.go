package git

import (
	"net/url"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	"golang.org/x/net/http/httpproxy"
)

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
