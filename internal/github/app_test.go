package github

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
)

const (
	validRSA = `-----BEGIN RSA PRIVATE KEY-----
MIICXQIBAAKBgQC1ZuFGlFeAFqeS6p04QsliOXG3NH1/lQC4UMXdQ0F73ciYBPKq
iQZcoyOu8a2Hsi5HvxDqR1rreTAkJ37C3ErrmKcE1CUJwxBVqkgE17Fzw63QBu0X
0OVtaUarG8Pd9HuKbXPK8HXFTEh6F5hoqmzCmG7cRHmagBeh1SqZm1awzQIDAQAB
AoGAChHZ84cMjGm1h6xKafMbJr61l0vso4Zr8c9aDHxNSEj5d6beqaTNm5rawj1c
Oqojc4whrj+jxmqFx5wBp2N/LRi7GhpPco4wy8gg2t/OjmcR+jTRJgT1x1Co9W58
U+O5c001YFTNoa1UUUBweqye/sX/k5GBCUt0V2G839Cn+8ECQQD2K2eZcyUeeBHT
/YhGAq++mmfVEkzMY7U+G59oeF038zXX+wtMwoKmC9/LHwVPWpnzL/oMu3zZqv4a
jzCOAdZpAkEAvKVas8KUctHUBvDoU6hq9bVyIZMZZnlBfysuFEeJLU8efp/n4KRO
93EyhcXe2FmOC/VzGbkiQobmAqVvIwTixQJBAIKYZE20GG0hpdOhHTqHElU79PnE
y5ljDDP204rI0Ctui5IZTNVcG5ObmQ5ZVqfSmPm66hz3GjUf0c6lSE0ODIECQHB0
silO6We5JggtPJICaCCpVawmIJIx3pWMjB+StXfJHoilknkb+ecQF+ofFsUqPb9r
Rn4jGwVFnYAeVq4tj3ECQQCyeMeCprz5AQ8HSd16Asd3zhv7N7olpb4XMIP6YZXy
udiSlDctMM/X3ZM2JN5M1rtAJ2WR3ZQtmWbOjZAbG2Eq
-----END RSA PRIVATE KEY-----`
	invalidRSA = `-----BEGIN RSA PRIVATE KEY-----
AQIDBA==
-----END RSA PRIVATE KEY-----`
	notRSA = `-----BEGIN PRIVATE KEY-----
MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQg3rAS658JOtxkOQ4L
7n8EebUpsbeV9Kx/iFGXwxjHPUOhRANCAAQCidzm5b6x5dXdMuq3b7sL52FdqkWx
ytV/UsL9lo9CSv5UTTAnRAjZkyFjDO3cieDA322H+5VQKI7moiKsfz6p
-----END PRIVATE KEY-----`
	testCACertPEM = `-----BEGIN CERTIFICATE-----
MIIDETCCAfmgAwIBAgIURQq4nwvZGxDntkJ+eqn/+pP3DfIwDQYJKoZIhvcNAQEL
BQAwGDEWMBQGA1UEAwwNZmxlZXQtdGVzdC1jYTAeFw0yNjA5MTUwNTUyMTVaFw0z
NjA5MTIwNTUyMTVaMBgxFjAUBgNVBAMMDWZsZWV0LXRlc3QtY2EwggEiMA0GCSqG
SIb3DQEBAQUAA4IBDwAwggEKAoIBAQDMMntHIC0HyWtEyvySoY7kkogXhhECKwYl
2LAzxs/0gkNZREGu0lw65OavJ/+OdAmwO/X4GEwOLgoJGitXJc8QUgzNsVDZ5//y
COrG4lltddl6QIY/jf1Tp2xWJOH8gbEmhAQ509nE2gBGM8zTrEPfmkLDJgeIXBTH
OHXxdaiFEtO9X/tErc9MeaLZtP+krOIoG+CDAySAkZ/UmdhWCscGtDfUggt9FIZ2
hiKVpSPJGzL7eFJhpksFm6ZrH30TMVKkAmEPxydH1tQTNQs+eFCEjEAweou81RII
nWEQOUkfIUwB/LlyMd5sDp2dGTwS2T1uLFUtHGIh5xtnjtYKYwgRAgMBAAGjUzBR
MB0GA1UdDgQWBBRbRL/zgrx+cXx7eNbfKUoZrgiSkjAfBgNVHSMEGDAWgBRbRL/z
grx+cXx7eNbfKUoZrgiSkjAPBgNVHRMBAf8EBTADAQH/MA0GCSqGSIb3DQEBCwUA
A4IBAQCzwO59PvKSngjg0mynuoXRUuIlOYlsTEQkwkqk9bdVnjTEuRHu9jCqkqbB
wEXvkUXyMXk/4TXUgqH+Kd/7+DIrr7QNdIaUcacYu4UCb6kazLu9SbE8dQtmqL0C
8yIzqZ6IyE8pf3JqzKhV/i3JegzSvr+H1aY3CPkwYxDWFBl2sdScI8YolNPGiReW
oac5sboVzKO3K4sRKarL3991RXLDX1U6PUMMSCXkloIxV4p+MjERck5MLRlpL3gl
J93I4ANMzOvoXJZ8wTMLKVseolKKbaGDOL7DqpzxpEqhFJCU3kasqLxr1tbtgj5u
UixxouFflOm/k5xi79QB9jFoQa9U
-----END CERTIFICATE-----`
)

type fakeRT struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeRT) RoundTrip(_ *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()

	body := `{"token":"abc123","expires_at":"2100-01-01T00:00:00Z"}`
	return &http.Response{
		StatusCode: http.StatusCreated,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

func TestGitHubApp_GetToken_Success(t *testing.T) {
	orig := http.DefaultTransport
	stub := &fakeRT{}
	http.DefaultTransport = stub
	t.Cleanup(func() { http.DefaultTransport = orig })

	app := NewApp("https://github.com/foo/bar", 123, 456, []byte(validRSA))

	token, err := app.GetToken(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetToken returned error: %v", err)
	}
	if token != "abc123" {
		t.Fatalf("unexpected token %q (want %q)", token, "abc123")
	}
	if stub.calls != 1 {
		t.Fatalf("expected exactly one outbound HTTP request, got %d", stub.calls)
	}
}

func TestGitHubApp_GetToken_NonGithubDotCom(t *testing.T) {
	// This test case does not seek successful authentication, but rather to validate that a GitRepo's repo URL is used
	// in authentication attempts, even if that URL does not feature host `github.com`.
	cases := []struct {
		name     string
		repoURL  string
		errRegex string
	}{
		{
			name:     "default base URL",
			repoURL:  "https://github.com/foo/bar",
			errRegex: `received non 2xx response status.*when fetching https://api\.github\.com/app/installations/.*/access_tokens`,
		},
		{
			name:     "non-github.com base URL",
			repoURL:  "https://fleetverse.ghe.com/foo/bar",
			errRegex: `could not refresh installation id.* lookup api\.fleetverse\.ghe\.com.* no such host`,
		},
		{
			name:     "non-github.com base URL with API prefix",
			repoURL:  "https://api.fleetverse.ghe.com/foo/bar",
			errRegex: `could not refresh installation id.* lookup api\.fleetverse\.ghe\.com.* no such host`,
		},
		{
			name:     "invalid URL",
			repoURL:  "://not-a-valid-url",
			errRegex: "failed to extract base Github App URL",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := NewApp(tc.repoURL, 123, 456, []byte(validRSA))

			_, err := app.GetToken(context.Background(), nil)
			if err == nil {
				t.Fatal("expected error when getting token, got nil")
			}

			re := regexp.MustCompile(tc.errRegex)
			if !re.MatchString(err.Error()) {
				t.Fatalf("expected error to match string %q, got %q", tc.errRegex, err.Error())
			}
		})
	}
}

func TestGitHubApp_GetToken_InvalidPEM(t *testing.T) {
	app := NewApp("https://github.com/foo/bar", 123, 456, []byte("definitely-not-a-PEM-block"))

	_, err := app.GetToken(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected error for invalid PEM, got nil")
	}
	const want = "pem decode failed for app"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}

func TestGitHubApp_GetToken_NotRSA(t *testing.T) {
	app := NewApp("https://github.com/foo/bar", 123, 456, []byte(notRSA))

	_, err := app.GetToken(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected error for not RSA PEM, got nil")
	}
	const want = "unsupported key type"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}

func TestGitHubApp_GetToken_InvalidRSA(t *testing.T) {
	app := NewApp("https://github.com/foo/bar", 123, 456, []byte(invalidRSA))

	_, err := app.GetToken(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected error for not RSA PEM, got nil")
	}
	const want = "invalid RSA key for app"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}

func TestTransportWithCABundle_EmptyBundle_ReturnsDefaultTransportUnchanged(t *testing.T) {
	tr, err := transportWithCABundle(nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if tr != http.DefaultTransport {
		t.Fatal("expected http.DefaultTransport to be returned unchanged for empty caBundle")
	}
}

func TestTransportWithCABundle_InvalidPEM_ReturnsError(t *testing.T) {
	_, err := transportWithCABundle([]byte("this is not a valid PEM certificate"))
	if err == nil {
		t.Fatal("expected error for invalid CA bundle PEM, got nil")
	}
}

func TestTransportWithCABundle_ValidBundle_BuildsTransportWithPoolAndMinVersion(t *testing.T) {
	tr, err := transportWithCABundle([]byte(testCACertPEM))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	httpTr, ok := tr.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", tr)
	}
	if httpTr.TLSClientConfig == nil {
		t.Fatal("expected TLSConfig to be set")
	}
	if httpTr.TLSClientConfig.RootCAs == nil {
		t.Fatal("expected RootCAs pool to be set")
	}
	if httpTr.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("expected MinVersion TLS 1.2, got %v", httpTr.TLSClientConfig.MinVersion)
	}
}

func TestTransportWithCABundle_NonHTTPTransportDefault_DoesNotPanic(t *testing.T) {
	orig := http.DefaultTransport
	http.DefaultTransport = &fakeRT{}
	t.Cleanup(func() { http.DefaultTransport = orig })

	_, err := transportWithCABundle([]byte(testCACertPEM))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
