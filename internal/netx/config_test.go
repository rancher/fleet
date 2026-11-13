package netx

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"testing"
)

const (
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

func TestTLSClientConfig_EmptyConfig(t *testing.T) {
	cfg, err := TLSConfig{}.TLSClientConfig()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.InsecureSkipVerify == true {
		t.Fatal("expected InsecureSkipVerify to be false")
	}
	if cfg.MinVersion < tls.VersionTLS12 {
		t.Fatalf("expected min TLS version %d", tls.VersionTLS12)
	}
	if cfg.RootCAs != nil {
		t.Fatal("CertPool is empty without a PEM")
	}
}

func TestTLSClientConfig_Empty_Config_And_CABundle(t *testing.T) {
	want, err := x509.SystemCertPool()
	if err != nil {
		t.Fatal(err)
	}
	if !want.AppendCertsFromPEM([]byte(testCACertPEM)) {
		t.Fatal("test CA did not parse")
	}

	cfg, err := TLSConfig{CABundle: []byte(testCACertPEM)}.TLSClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.RootCAs.Equal(want) {
		t.Fatal("expected RootCAs to be the system pool plus the test CA")
	}
}

func TestTLSClientConfig_EmptyCABundle(t *testing.T) {
	cfg, err := TLSConfig{CABundle: []byte{}}.TLSClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RootCAs != nil {
		t.Fatal("expected RootCAs to be nil for an empty CABundle")
	}
}

func TestTLSClientConfig_Invalid_PEM(t *testing.T) {
	cfg, err := TLSConfig{CABundle: []byte(`-----BEGIN CERTIFICATE-----`)}.TLSClientConfig()
	if err == nil {
		t.Fatal("expected an error for a CA bundle without valid certificates")
	}
	if cfg != nil {
		t.Fatal("expected no tls.Config on error")
	}
}

func TestWithProxyCA(t *testing.T) {
	tests := []struct {
		name     string
		cfg      TLSConfig
		proxyEnv string
		want     []byte
	}{
		{
			name: "no proxy CA keeps an empty bundle",
			cfg:  TLSConfig{},
			want: []byte{},
		},
		{
			name: "no proxy CA keeps the bundle",
			cfg:  TLSConfig{CABundle: []byte(testCACertPEM)},
			want: []byte(testCACertPEM),
		},
		{
			name:     "invalid proxy CA is ignored",
			cfg:      TLSConfig{CABundle: []byte(testCACertPEM)},
			proxyEnv: "not a certificate",
			want:     []byte(testCACertPEM),
		},
		{
			name:     "proxy CA with an empty bundle",
			cfg:      TLSConfig{},
			proxyEnv: testCACertPEM,
			want:     []byte(testCACertPEM),
		},
		{
			name:     "proxy CA after a bundle without a trailing newline",
			cfg:      TLSConfig{CABundle: []byte(testCACertPEM)},
			proxyEnv: testCACertPEM,
			want:     []byte(testCACertPEM + "\n" + testCACertPEM),
		},
		{
			name:     "proxy CA after a bundle with a trailing newline",
			cfg:      TLSConfig{CABundle: []byte(testCACertPEM + "\n")},
			proxyEnv: testCACertPEM,
			want:     []byte(testCACertPEM + "\n" + testCACertPEM),
		},
		{
			name:     "InsecureSkipVerify and Certificates are kept",
			cfg:      TLSConfig{InsecureSkipVerify: true, Certificates: []tls.Certificate{{}}},
			proxyEnv: testCACertPEM,
			want:     []byte(testCACertPEM),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(ProxyCABundleEnvVar, test.proxyEnv)

			cfg := test.cfg.WithProxyCA()
			if !bytes.Equal(cfg.CABundle, test.want) {
				t.Fatalf("CABundle = %q, want %q", cfg.CABundle, test.want)
			}
			if cfg.InsecureSkipVerify != test.cfg.InsecureSkipVerify {
				t.Fatal("InsecureSkipVerify changed")
			}
			if len(cfg.Certificates) != len(test.cfg.Certificates) {
				t.Fatal("Certificates changed")
			}
		})
	}
}

func TestWithProxyCA_DoesNotModifyCallerBundle(t *testing.T) {
	t.Setenv(ProxyCABundleEnvVar, testCACertPEM)

	// Spare capacity lets a careless append write into the caller's array.
	base := make([]byte, 0, 2*len(testCACertPEM)+1)
	base = append(base, testCACertPEM...)

	cfg := TLSConfig{CABundle: base}.WithProxyCA()
	if len(cfg.CABundle) <= len(base) {
		t.Fatal("expected the proxy CA to be appended")
	}
	if spare := base[len(base):cap(base)]; bytes.ContainsAny(spare, "-\n") {
		t.Fatal("WithProxyCA wrote into the caller's CABundle array")
	}
}
