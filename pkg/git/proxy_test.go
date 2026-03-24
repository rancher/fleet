package git

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

// startFakeProxy starts a minimal HTTP CONNECT proxy on a random local port.
// For each accepted connection it reads a CONNECT request, calls onConnect (if
// non-nil) so the test can inspect or override the response, and then pipes the
// two connections together until both sides close.
//
// onConnect semantics:
//   - If the callback writes a non-200 status the connection is aborted.
//   - If the callback writes a 200 status the tunnel proceeds normally.
//   - If the callback writes nothing (statusCode stays 0) the helper defaults
//     to 200, so inspection-only callbacks work without any explicit write.
//
// Returns the listener address and a cancel function that shuts the proxy down.
func startFakeProxy(t *testing.T, onConnect func(w http.ResponseWriter, r *http.Request)) (string, func()) {
	t.Helper()

	var wg sync.WaitGroup
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("startFakeProxy: listen: %v", err)
	}

	wg.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				handleProxyConn(t, c, onConnect)
			}(conn)
		}
	})

	stop := func() {
		ln.Close()
		wg.Wait()
	}
	return ln.Addr().String(), stop
}

// handleProxyConn processes one client connection arriving at the fake proxy.
func handleProxyConn(t *testing.T, clientConn net.Conn, onConnect func(http.ResponseWriter, *http.Request)) {
	t.Helper()
	defer clientConn.Close()

	br := bufio.NewReader(clientConn)
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}

	if req.Method != http.MethodConnect {
		fmt.Fprintf(clientConn, "HTTP/1.1 405 Method Not Allowed\r\n\r\n")
		return
	}

	if onConnect != nil {
		rw := &simpleResponseWriter{conn: clientConn}
		onConnect(rw, req)
		switch {
		case rw.statusCode == 0:
			// Inspection-only callback: the callback did not write anything, so
			// default to accepting the connection.
			fmt.Fprintf(clientConn, "HTTP/1.1 200 Connection established\r\n\r\n")
		case rw.statusCode != http.StatusOK:
			// Callback actively rejected the connection.
			return
		}
		// rw.statusCode == 200: callback already sent the response, proceed.
	} else {
		// Default: accept all CONNECT requests.
		fmt.Fprintf(clientConn, "HTTP/1.1 200 Connection established\r\n\r\n")
	}

	// Dial the target.
	targetConn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", req.Host)
	if err != nil {
		return
	}
	defer targetConn.Close()

	// Pipe in both directions. When client→target finishes, close targetConn
	// so the target→client goroutine unblocks immediately.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(targetConn, br)
		targetConn.Close()
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(clientConn, targetConn)
	}()
	wg.Wait()
}

// simpleResponseWriter is a minimal http.ResponseWriter that writes directly to
// a net.Conn. It is only used to let onConnect produce an HTTP response.
type simpleResponseWriter struct {
	conn       net.Conn
	statusCode int
	header     http.Header
	once       sync.Once
}

func (w *simpleResponseWriter) Header() http.Header {
	w.once.Do(func() { w.header = make(http.Header) })
	return w.header
}

func (w *simpleResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	fmt.Fprintf(w.conn, "HTTP/1.1 %d %s\r\n\r\n", code, http.StatusText(code))
}

func (w *simpleResponseWriter) Write(b []byte) (int, error) {
	if w.statusCode == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.conn.Write(b)
}

// startEchoServer starts a TCP server that echoes everything it receives back
// to the sender. It is used as the "target" that the proxy tunnels to.
// startStallingProxy starts a TCP listener that accepts connections and reads
// the CONNECT request line, but never sends any response. This simulates a
// proxy that stalls mid-handshake and is used to verify that ctx cancellation
// and deadline expiry correctly abort DialContext.
func startStallingProxy(t *testing.T) (string, func()) {
	t.Helper()

	var wg sync.WaitGroup
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("startStallingProxy: listen: %v", err)
	}

	wg.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()
				// Drain the CONNECT request so the client's Write completes,
				// then block indefinitely – simulating a stalled proxy.
				br := bufio.NewReader(c)
				for {
					line, err := br.ReadString('\n')
					if err != nil || line == "\r\n" {
						break
					}
				}
				// Block until the connection is closed from the other side.
				buf := make([]byte, 1)
				_, _ = c.Read(buf)
			}(conn)
		}
	})

	stop := func() {
		ln.Close()
		wg.Wait()
	}
	return ln.Addr().String(), stop
}

func startEchoServer(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("startEchoServer: listen: %v", err)
	}
	var wg sync.WaitGroup

	wg.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						_, _ = c.Write(buf[:n])
					}
					if err != nil {
						return
					}
				}
			}(conn)
		}
	})

	stop := func() {
		ln.Close()
		wg.Wait()
	}
	return ln.Addr().String(), stop
}

// parseURL is a test helper that fatally fails if url.Parse returns an error.
func parseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parseURL(%q): %v", raw, err)
	}
	return u
}

// mustDialContext calls newHTTPConnectDialer and asserts the returned
// proxy.Dialer also implements proxy.ContextDialer. Both conditions are fatal
// if they fail. This keeps individual tests free of repetitive boilerplate.
func mustDialContext(t *testing.T, proxyURL *url.URL, forward proxy.Dialer) proxy.ContextDialer {
	t.Helper()
	d, err := newHTTPConnectDialer(proxyURL, forward)
	if err != nil {
		t.Fatalf("newHTTPConnectDialer: %v", err)
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		t.Fatalf("newHTTPConnectDialer returned %T which does not implement proxy.ContextDialer", d)
	}
	return cd
}

// TestHTTPConnectDialer_TunnelsThroughProxy verifies that DialContext
// successfully establishes a CONNECT tunnel and that data can be exchanged
// through it.
func TestHTTPConnectDialer_TunnelsThroughProxy(t *testing.T) {
	echoAddr, stopEcho := startEchoServer(t)
	defer stopEcho()

	proxyAddr, stopProxy := startFakeProxy(t, nil)
	defer stopProxy()

	d := &httpConnectDialer{
		proxyURL: parseURL(t, "http://"+proxyAddr),
		forward:  proxy.Direct,
	}

	conn, err := d.DialContext(context.Background(), "tcp", echoAddr)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	defer conn.Close()

	msg := "hello through proxy"
	if _, err := fmt.Fprint(conn, msg); err != nil {
		t.Fatalf("Write: %v", err)
	}

	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := string(buf); got != msg {
		t.Errorf("echo mismatch: got %q, want %q", got, msg)
	}
}

// TestHTTPConnectDialer_Dial checks that the non-context Dial method works too.
func TestHTTPConnectDialer_Dial(t *testing.T) {
	echoAddr, stopEcho := startEchoServer(t)
	defer stopEcho()

	proxyAddr, stopProxy := startFakeProxy(t, nil)
	defer stopProxy()

	d := &httpConnectDialer{
		proxyURL: parseURL(t, "http://"+proxyAddr),
		forward:  proxy.Direct,
	}

	conn, err := d.Dial("tcp", echoAddr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	msg := "dial no ctx"
	fmt.Fprint(conn, msg)
	buf := make([]byte, len(msg))

	_, err = io.ReadFull(conn, buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf) != msg {
		t.Errorf("got %q, want %q", string(buf), msg)
	}
}

// TestHTTPConnectDialer_SendsProxyAuthorization verifies that the
// Proxy-Authorization header is set when the proxy URL contains credentials.
func TestHTTPConnectDialer_SendsProxyAuthorization(t *testing.T) {
	var gotAuth string
	echoAddr, stopEcho := startEchoServer(t)
	defer stopEcho()

	proxyAddr, stopProxy := startFakeProxy(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Proxy-Authorization")
		w.WriteHeader(http.StatusOK)
	})
	defer stopProxy()

	d := &httpConnectDialer{
		proxyURL: parseURL(t, "http://alice:s3cr3t@"+proxyAddr),
		forward:  proxy.Direct,
	}

	conn, err := d.DialContext(context.Background(), "tcp", echoAddr)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	conn.Close()

	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Fatalf("expected Basic auth header, got: %q", gotAuth)
	}
}

// TestHTTPConnectDialer_NoCredentials verifies that no Proxy-Authorization
// header is sent when the proxy URL has no userinfo.
func TestHTTPConnectDialer_NoCredentials(t *testing.T) {
	var gotAuth string
	echoAddr, stopEcho := startEchoServer(t)
	defer stopEcho()

	proxyAddr, stopProxy := startFakeProxy(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Proxy-Authorization")
		w.WriteHeader(http.StatusOK)
	})
	defer stopProxy()

	d := &httpConnectDialer{
		proxyURL: parseURL(t, "http://"+proxyAddr),
		forward:  proxy.Direct,
	}

	conn, err := d.DialContext(context.Background(), "tcp", echoAddr)
	if err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	conn.Close()

	if gotAuth != "" {
		t.Errorf("expected no auth header, got: %q", gotAuth)
	}
}

// TestHTTPConnectDialer_NonOKStatusReturnsError checks that a non-200 proxy
// response is turned into a descriptive error.
func TestHTTPConnectDialer_NonOKStatusReturnsError(t *testing.T) {
	echoAddr, stopEcho := startEchoServer(t)
	defer stopEcho()

	proxyAddr, stopProxy := startFakeProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	defer stopProxy()

	d := &httpConnectDialer{
		proxyURL: parseURL(t, "http://"+proxyAddr),
		forward:  proxy.Direct,
	}

	_, err := d.DialContext(context.Background(), "tcp", echoAddr)
	if err == nil {
		t.Fatal("expected error for 403 response, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected error to mention 403, got: %v", err)
	}
}

// TestHTTPConnectDialer_DefaultPort_HTTP verifies that when no port is given in
// an http:// proxy URL the dialer falls back to port 3128 (Squid default).
func TestHTTPConnectDialer_DefaultPort_HTTP(t *testing.T) {
	// We cannot actually connect to port 3128 in a unit test, so we just verify
	// the address that would be dialled by injecting a recording forward dialer.
	var dialledAddr string
	rec := &recordingDialer{dial: func(network, addr string) (net.Conn, error) {
		dialledAddr = addr
		// Return a fake conn that writes a valid 200 response.
		return newFakeProxyConn(), nil
	}}

	d := mustDialContext(t, parseURL(t, "http://squid.example.com"), rec)

	// The connection will fail after the CONNECT exchange because the fake
	// conn does not actually forward anything, but we only care about the
	// address the dialer tried to reach.
	conn, _ := d.DialContext(context.Background(), "tcp", "git.example.com:22")
	if conn != nil {
		conn.Close()
	}

	if !strings.HasSuffix(dialledAddr, ":3128") {
		t.Errorf("expected port 3128 as default for http://, got addr %q", dialledAddr)
	}
}

// TestHTTPConnectDialer_DefaultPort_HTTPS verifies that when no port is given
// in an https:// proxy URL the dialer falls back to port 443.
func TestHTTPConnectDialer_DefaultPort_HTTPS(t *testing.T) {
	var dialledAddr string
	rec := &recordingDialer{dial: func(network, addr string) (net.Conn, error) {
		dialledAddr = addr
		return newFakeProxyConn(), nil
	}}

	d := mustDialContext(t, parseURL(t, "https://squid.example.com"), rec)

	conn, _ := d.DialContext(context.Background(), "tcp", "git.example.com:22")
	if conn != nil {
		conn.Close()
	}

	if !strings.HasSuffix(dialledAddr, ":443") {
		t.Errorf("expected port 443 as default for https://, got addr %q", dialledAddr)
	}
}

// TestHTTPConnectDialer_ContextCancelled checks that a context cancelled before
// the proxy responds causes DialContext to return ctx.Err().
// This test exercises cancellation during the forward-dial phase (before the
// TCP connection to the proxy is established).
func TestHTTPConnectDialer_ContextCancelled(t *testing.T) {
	// Use a forward dialer that blocks until the test context is done.
	blocking := &recordingDialer{dial: func(network, addr string) (net.Conn, error) {
		<-t.Context().Done()
		return nil, fmt.Errorf("should not reach here")
	}}

	d := mustDialContext(t, parseURL(t, "http://127.0.0.1:3128"), blocking)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := d.DialContext(ctx, "tcp", "git.example.com:22")
	if err == nil {
		t.Fatal("expected error due to context cancellation, got nil")
	}
}

// TestNewHTTPConnectDialer_NonContextDialerReturnsError verifies that passing a
// forward dialer that only implements proxy.Dialer (not proxy.ContextDialer)
// returns an error, since we require ContextDialer for proper context support.
func TestNewHTTPConnectDialer_NonContextDialerReturnsError(t *testing.T) {
	u := parseURL(t, "http://proxy.example.com:3128")
	plain := &plainDialer{dial: func(network, addr string) (net.Conn, error) {
		return nil, nil
	}}
	_, err := newHTTPConnectDialer(u, plain)
	if err == nil {
		t.Fatal("expected error when forward dialer does not implement proxy.ContextDialer, got nil")
	}
	if !strings.Contains(err.Error(), "proxy.ContextDialer") {
		t.Errorf("expected error to mention proxy.ContextDialer, got: %v", err)
	}
}

// plainDialer implements only proxy.Dialer (not proxy.ContextDialer), used to
// verify that newHTTPConnectDialer rejects non-context-aware dialers.
type plainDialer struct {
	dial func(network, addr string) (net.Conn, error)
}

func (p *plainDialer) Dial(network, addr string) (net.Conn, error) {
	return p.dial(network, addr)
}

// TestHTTPConnectDialer_ContextCancelledDuringHandshake checks that a context
// cancelled while the proxy has accepted the TCP connection but not yet sent
// any CONNECT response causes DialContext to return promptly with an error.
//
// req.Write(conn) and http.ReadResponse(...) do not observe ctx on their own,
// so DialContext closes the conn from a watchdog goroutine when ctx.Done()
// fires, which unblocks any in-progress Read or Write immediately.
func TestHTTPConnectDialer_ContextCancelledDuringHandshake(t *testing.T) {
	// stallingProxy accepts the TCP connection and reads the CONNECT request,
	// but deliberately never writes a response, simulating a stalled or
	// misbehaving proxy.
	proxyAddr, stopProxy := startStallingProxy(t)
	defer stopProxy()

	d := mustDialContext(t, parseURL(t, "http://"+proxyAddr), proxy.Direct)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := d.DialContext(ctx, "tcp", "git.example.com:22")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error due to context cancellation during handshake, got nil")
	}
	// The call must return well before the 5-second safety ceiling, and the
	// error must be (or wrap) a context error.
	if elapsed > 2*time.Second {
		t.Errorf("DialContext took %v; expected it to abort near the 100 ms timeout", elapsed)
	}
	if ctx.Err() == nil {
		t.Errorf("expected ctx to be done after timeout, but ctx.Err() is nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("expected error to wrap a context error, got: %v", err)
	}
}

// TestHTTPConnectDialer_DeadlineExpiresDuringHandshake is the deadline-based
// analogue of TestHTTPConnectDialer_ContextCancelledDuringHandshake. It uses
// context.WithDeadline to confirm that deadline expiry (not just explicit
// cancellation) also causes DialContext to abort promptly.
func TestHTTPConnectDialer_DeadlineExpiresDuringHandshake(t *testing.T) {
	proxyAddr, stopProxy := startStallingProxy(t)
	defer stopProxy()

	d := mustDialContext(t, parseURL(t, "http://"+proxyAddr), proxy.Direct)

	deadline := time.Now().Add(100 * time.Millisecond)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	start := time.Now()
	_, err := d.DialContext(ctx, "tcp", "git.example.com:22")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error due to deadline expiry during handshake, got nil")
	}
	if elapsed > 2*time.Second {
		t.Errorf("DialContext took %v; expected it to abort near the 100 ms deadline", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("expected error to wrap a context error, got: %v", err)
	}
}

// TestNewHTTPConnectDialer_NilForwardUsesDirectDialer verifies that a nil
// forward dialer is replaced by proxy.Direct.
func TestNewHTTPConnectDialer_NilForwardUsesDirectDialer(t *testing.T) {
	u := parseURL(t, "http://proxy.example.com:3128")
	d, err := newHTTPConnectDialer(u, nil)
	if err != nil {
		t.Fatalf("newHTTPConnectDialer: %v", err)
	}
	hd, ok := d.(*httpConnectDialer)
	if !ok {
		t.Fatalf("expected *httpConnectDialer, got %T", d)
	}
	if hd.forward != proxy.Direct {
		t.Errorf("expected forward to be proxy.Direct when nil is passed")
	}
}

// TestRegistration verifies that proxy.FromURL accepts http:// and https://
// scheme URLs without returning "unknown scheme" and that the returned dialer
// satisfies proxy.ContextDialer (as required by go-git's SSH transport).
func TestRegistration(t *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			u := parseURL(t, scheme+"://proxy.example.com:3128")
			d, err := proxy.FromURL(u, proxy.Direct)
			if err != nil {
				t.Fatalf("proxy.FromURL with scheme %q: %v", scheme, err)
			}
			if _, ok := d.(proxy.ContextDialer); !ok {
				t.Errorf("dialer for scheme %q does not implement proxy.ContextDialer", scheme)
			}
		})
	}
}

// startFakeTLSProxy starts a TLS-wrapped HTTP CONNECT proxy using
// httptest.NewTLSServer. It returns the proxy address, a *tls.Config suitable
// for use as httpConnectDialer.tlsConfig (it trusts the test server's
// self-signed certificate), and a stop function.
func startFakeTLSProxy(t *testing.T) (string, *tls.Config, func()) {
	t.Helper()
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "expected CONNECT", http.StatusMethodNotAllowed)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijacking not supported", http.StatusInternalServerError)
			return
		}
		clientConn, br, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer clientConn.Close()

		fmt.Fprintf(clientConn, "HTTP/1.1 200 Connection established\r\n\r\n")

		dailer := &net.Dialer{}
		targetConn, err := dailer.DialContext(r.Context(), "tcp", r.Host)
		if err != nil {
			return
		}
		defer targetConn.Close()

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			// br may have buffered bytes from after the CONNECT request.
			_, _ = io.Copy(targetConn, br)
			// Close targetConn so the other goroutine's io.Copy unblocks.
			targetConn.Close()
		}()
		go func() {
			defer wg.Done()
			_, _ = io.Copy(clientConn, targetConn)
		}()
		wg.Wait()
	}))
	ts.StartTLS()
	clientTLSConfig := ts.Client().Transport.(*http.Transport).TLSClientConfig
	return ts.Listener.Addr().String(), clientTLSConfig, ts.Close
}

// TestHTTPConnectDialer_TLSForHTTPSProxy verifies that when the proxy URL uses
// the https:// scheme the dialer performs a TLS handshake with the proxy before
// sending the CONNECT request, protecting credentials in transit.
func TestHTTPConnectDialer_TLSForHTTPSProxy(t *testing.T) {
	echoAddr, stopEcho := startEchoServer(t)
	defer stopEcho()

	proxyAddr, clientTLSConfig, stopProxy := startFakeTLSProxy(t)
	defer stopProxy()

	d := &httpConnectDialer{
		proxyURL:  parseURL(t, "https://"+proxyAddr),
		forward:   proxy.Direct,
		tlsConfig: clientTLSConfig,
	}

	conn, err := d.DialContext(context.Background(), "tcp", echoAddr)
	if err != nil {
		t.Fatalf("DialContext through TLS proxy: %v", err)
	}
	defer conn.Close()

	msg := "hello through TLS proxy"
	if _, err := fmt.Fprint(conn, msg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := string(buf); got != msg {
		t.Errorf("echo mismatch: got %q, want %q", got, msg)
	}
}

// TestHTTPConnectDialer_PlainProxyRejectedForHTTPSScheme verifies that when an
// https:// proxy URL is configured but the proxy only accepts plain TCP (no
// TLS), the TLS handshake fails — confirming that TLS is actually attempted.
func TestHTTPConnectDialer_PlainProxyRejectedForHTTPSScheme(t *testing.T) {
	proxyAddr, stopProxy := startFakeProxy(t, nil)
	defer stopProxy()

	d := &httpConnectDialer{
		proxyURL:  parseURL(t, "https://"+proxyAddr),
		forward:   proxy.Direct,
		tlsConfig: &tls.Config{InsecureSkipVerify: true}, // skip cert check; we want the record-layer error
	}

	_, err := d.DialContext(context.Background(), "tcp", "git.example.com:22")
	if err == nil {
		t.Fatal("expected TLS handshake error when connecting to a plain-HTTP proxy with https:// scheme")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "tls") {
		t.Logf("got error (does not mention tls): %v", err)
	}
}

// --------------------------------------------------------------------------
// helpers
// --------------------------------------------------------------------------

// recordingDialer is a proxy.ContextDialer whose DialContext implementation is
// provided by a field so individual tests can customize it. It also satisfies
// proxy.Dialer via the Dial shim so it can be passed to newHTTPConnectDialer.
type recordingDialer struct {
	dial func(network, addr string) (net.Conn, error)
}

func (r *recordingDialer) Dial(network, addr string) (net.Conn, error) {
	return r.dial(network, addr)
}

func (r *recordingDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := r.dial(network, addr)
		ch <- result{conn, err}
	}()
	select {
	case <-ctx.Done():
		go func() {
			if res := <-ch; res.conn != nil {
				res.conn.Close()
			}
		}()
		return nil, ctx.Err()
	case res := <-ch:
		return res.conn, res.err
	}
}

// fakeProxyConn is a net.Conn that immediately returns a "200 Connection
// established" response and then behaves like /dev/null. It is used when tests
// only care about what address the dialer tried to reach, not about subsequent
// data exchange.
type fakeProxyConn struct {
	reader *strings.Reader
}

func newFakeProxyConn() *fakeProxyConn {
	return &fakeProxyConn{
		reader: strings.NewReader("HTTP/1.1 200 Connection established\r\n\r\n"),
	}
}

func (f *fakeProxyConn) Read(b []byte) (int, error)         { return f.reader.Read(b) }
func (f *fakeProxyConn) Write(b []byte) (int, error)        { return len(b), nil }
func (f *fakeProxyConn) Close() error                       { return nil }
func (f *fakeProxyConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (f *fakeProxyConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (f *fakeProxyConn) SetDeadline(t time.Time) error      { return nil }
func (f *fakeProxyConn) SetReadDeadline(t time.Time) error  { return nil }
func (f *fakeProxyConn) SetWriteDeadline(t time.Time) error { return nil }

// --------------------------------------------------------------------------
// proxyOptsFromEnvironment tests
// --------------------------------------------------------------------------

func TestProxyOptsFromEnvironment(t *testing.T) {
	tests := []struct {
		name     string
		repoURL  string
		env      map[string]string
		wantURL  string
		wantUser string
		wantPass string
	}{
		{
			name:    "no env vars set returns empty",
			repoURL: "https://github.com/org/repo.git",
			env:     map[string]string{},
			wantURL: "",
		},
		{
			name:    "https repo picks HTTPS_PROXY",
			repoURL: "https://github.com/org/repo.git",
			env:     map[string]string{"HTTPS_PROXY": "http://proxy.example.com:3128"},
			wantURL: "http://proxy.example.com:3128",
		},
		{
			name:    "https repo ignores HTTP_PROXY",
			repoURL: "https://github.com/org/repo.git",
			env:     map[string]string{"HTTP_PROXY": "http://other.example.com:3128"},
			wantURL: "",
		},
		{
			name:    "http repo picks HTTP_PROXY",
			repoURL: "http://github.com/org/repo.git",
			env:     map[string]string{"HTTP_PROXY": "http://proxy.example.com:3128"},
			wantURL: "http://proxy.example.com:3128",
		},
		{
			name:    "http repo ignores HTTPS_PROXY",
			repoURL: "http://github.com/org/repo.git",
			env:     map[string]string{"HTTPS_PROXY": "http://proxy.example.com:3128"},
			wantURL: "",
		},
		{
			name:    "ssh:// repo prefers HTTPS_PROXY",
			repoURL: "ssh://git@github.com/org/repo.git",
			env: map[string]string{
				"HTTPS_PROXY": "http://sshproxy.example.com:3128",
				"HTTP_PROXY":  "http://other.example.com:3128",
			},
			wantURL: "http://sshproxy.example.com:3128",
		},
		{
			name:    "ssh:// repo falls back to HTTP_PROXY when HTTPS_PROXY unset",
			repoURL: "ssh://git@github.com/org/repo.git",
			env:     map[string]string{"HTTP_PROXY": "http://fallback.example.com:3128"},
			wantURL: "http://fallback.example.com:3128",
		},
		{
			name:    "scp-style repo prefers HTTPS_PROXY",
			repoURL: "git@github.com:org/repo.git",
			env:     map[string]string{"HTTPS_PROXY": "http://scpproxy.example.com:3128"},
			wantURL: "http://scpproxy.example.com:3128",
		},
		{
			name:    "lowercase https_proxy is respected",
			repoURL: "https://github.com/org/repo.git",
			env:     map[string]string{"https_proxy": "http://lower.example.com:3128"},
			wantURL: "http://lower.example.com:3128",
		},
		{
			name:     "proxy credentials are extracted",
			repoURL:  "ssh://git@github.com/org/repo.git",
			env:      map[string]string{"HTTPS_PROXY": "http://alice:s3cr3t@proxy.example.com:3128"},
			wantURL:  "http://alice:s3cr3t@proxy.example.com:3128",
			wantUser: "alice",
			wantPass: "s3cr3t",
		},
		{
			name:    "host in NO_PROXY returns empty",
			repoURL: "https://internal.example.com/org/repo.git",
			env: map[string]string{
				"HTTPS_PROXY": "http://proxy.example.com:3128",
				"NO_PROXY":    "internal.example.com",
			},
			wantURL: "",
		},
		{
			name:    "host not in NO_PROXY uses proxy",
			repoURL: "https://external.example.com/org/repo.git",
			env: map[string]string{
				"HTTPS_PROXY": "http://proxy.example.com:3128",
				"NO_PROXY":    "internal.example.com",
			},
			wantURL: "http://proxy.example.com:3128",
		},
		{
			name:    "empty repoURL returns empty",
			repoURL: "",
			env:     map[string]string{"HTTPS_PROXY": "http://proxy.example.com:3128"},
			wantURL: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy"} {
				t.Setenv(k, "")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			got := ProxyOptsFromEnvironment(tt.repoURL)

			if got.URL != tt.wantURL {
				t.Errorf("URL: got %q, want %q", got.URL, tt.wantURL)
			}
			if got.Username != tt.wantUser {
				t.Errorf("Username: got %q, want %q", got.Username, tt.wantUser)
			}
			if got.Password != tt.wantPass {
				t.Errorf("Password: got %q, want %q", got.Password, tt.wantPass)
			}
		})
	}
}

// TestProxyOptsFromEnvironment_ReturnType verifies the return type is
// transport.ProxyOptions so callers can embed it without a cast.
func TestProxyOptsFromEnvironment_ReturnType(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://proxy.example.com:3128")
	var _ = ProxyOptsFromEnvironment("https://github.com/org/repo.git")
}

// --------------------------------------------------------------------------
// PROXY_CA_BUNDLE tests
// --------------------------------------------------------------------------

// TestNewHTTPConnectDialer_CABundle_IgnoredForHTTPScheme verifies that
// PROXY_CA_BUNDLE has no effect when the proxy scheme is http (plaintext).
// A TLS config is only relevant for https proxies; setting it for an http
// proxy would be a no-op but we assert it stays nil to keep the code path clean.
func TestNewHTTPConnectDialer_CABundle_IgnoredForHTTPScheme(t *testing.T) {
	t.Setenv(ProxyCABundleEnvVar, "-----BEGIN CERTIFICATE-----\ndummy\n-----END CERTIFICATE-----")

	d, err := newHTTPConnectDialer(parseURL(t, "http://proxy.example.com:3128"), proxy.Direct)
	if err != nil {
		t.Fatalf("newHTTPConnectDialer: %v", err)
	}
	hd := d.(*httpConnectDialer)
	if hd.tlsConfig != nil {
		t.Error("expected tlsConfig to be nil for http:// proxy even when PROXY_CA_BUNDLE is set")
	}
}

// TestNewHTTPConnectDialer_CABundle_AppliedForHTTPSScheme verifies that when
// PROXY_CA_BUNDLE is set and the proxy URL uses https://, a custom TLS config
// is built and stored in the dialer.
func TestNewHTTPConnectDialer_CABundle_AppliedForHTTPSScheme(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ts.Close()
	caDER := ts.TLS.Certificates[0].Certificate[0]
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	t.Setenv(ProxyCABundleEnvVar, string(caPEM))

	d, err := newHTTPConnectDialer(parseURL(t, "https://proxy.example.com:3128"), proxy.Direct)
	if err != nil {
		t.Fatalf("newHTTPConnectDialer: %v", err)
	}
	hd := d.(*httpConnectDialer)
	if hd.tlsConfig == nil {
		t.Error("expected tlsConfig to be set for https:// proxy when PROXY_CA_BUNDLE is non-empty")
	}
}

// TestNewHTTPConnectDialer_CABundle_NoEnvVar verifies that without PROXY_CA_BUNDLE
// the dialer's tlsConfig is nil, even for an https:// proxy.
func TestNewHTTPConnectDialer_CABundle_NoEnvVar(t *testing.T) {
	t.Setenv(ProxyCABundleEnvVar, "")

	d, err := newHTTPConnectDialer(parseURL(t, "https://proxy.example.com:3128"), proxy.Direct)
	if err != nil {
		t.Fatalf("newHTTPConnectDialer: %v", err)
	}
	hd := d.(*httpConnectDialer)
	if hd.tlsConfig != nil {
		t.Error("expected tlsConfig to be nil when PROXY_CA_BUNDLE is empty")
	}
}

// TestHTTPConnectDialer_ProxyCABundle_TrustsCustomCA is an end-to-end test.
// It starts a TLS-wrapped CONNECT proxy backed by httptest, extracts the
// self-signed CA cert as PEM, injects it via PROXY_CA_BUNDLE, and then calls
// newHTTPConnectDialer — which must pick it up and trust the proxy without any
// explicit tlsConfig field.  The test verifies the full tunnel works by
// exchanging a message with a plain echo server on the other side.
func TestHTTPConnectDialer_ProxyCABundle_TrustsCustomCA(t *testing.T) {
	echoAddr, stopEcho := startEchoServer(t)
	defer stopEcho()

	// Inline TLS CONNECT proxy (same logic as startFakeTLSProxy but we need
	// access to the *httptest.Server to extract its certificate).
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "expected CONNECT", http.StatusMethodNotAllowed)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijacking not supported", http.StatusInternalServerError)
			return
		}
		clientConn, br, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer clientConn.Close()
		fmt.Fprintf(clientConn, "HTTP/1.1 200 Connection established\r\n\r\n")
		targetConn, err := (&net.Dialer{}).DialContext(r.Context(), "tcp", r.Host)
		if err != nil {
			return
		}
		defer targetConn.Close()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = io.Copy(targetConn, br)
			targetConn.Close()
		}()
		go func() {
			defer wg.Done()
			_, _ = io.Copy(clientConn, targetConn)
		}()
		wg.Wait()
	}))
	ts.StartTLS()
	defer ts.Close()

	// Extract the proxy server's leaf certificate as PEM.
	caDER := ts.TLS.Certificates[0].Certificate[0]
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	// Inject the CA via the environment variable — no explicit tlsConfig.
	t.Setenv(ProxyCABundleEnvVar, string(caPEM))

	d, err := newHTTPConnectDialer(parseURL(t, "https://"+ts.Listener.Addr().String()), proxy.Direct)
	if err != nil {
		t.Fatalf("newHTTPConnectDialer: %v", err)
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		t.Fatal("expected proxy.ContextDialer")
	}

	conn, err := cd.DialContext(context.Background(), "tcp", echoAddr)
	if err != nil {
		t.Fatalf("DialContext through TLS proxy with PROXY_CA_BUNDLE: %v", err)
	}
	defer conn.Close()

	msg := "hello via CA bundle"
	if _, err := fmt.Fprint(conn, msg); err != nil {
		t.Fatalf("Write: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got := string(buf); got != msg {
		t.Errorf("echo mismatch: got %q, want %q", got, msg)
	}
}

// --------------------------------------------------------------------------
// hostFromRepoURL tests
// --------------------------------------------------------------------------

func TestHostFromRepoURL(t *testing.T) {
	tests := []struct {
		name    string
		repoURL string
		want    string
	}{
		{"https URL", "https://github.com/org/repo.git", "github.com"},
		{"https URL with port", "https://github.com:8443/org/repo.git", "github.com"},
		{"http URL", "http://gitlab.com/org/repo.git", "gitlab.com"},
		{"ssh URL", "ssh://git@github.com/org/repo.git", "github.com"},
		{"scp-style", "git@github.com:org/repo.git", "github.com"},
		{"scp-style with user", "user@bitbucket.org:org/repo.git", "bitbucket.org"},
		{"bare hostname fallback", "github.com", "github.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hostFromRepoURL(tt.repoURL)
			if got != tt.want {
				t.Errorf("hostFromRepoURL(%q) = %q, want %q", tt.repoURL, got, tt.want)
			}
		})
	}
}
