package netx

import (
	"crypto/tls"
	"crypto/x509"
)

// Config holds TLS settings for outbound connections.
type Config struct {
	// CABundle holds PEM CAs trusted in addition to the system pool.
	CABundle []byte
	InsecureSkipVerify bool
}

// TLSConfig returns a tls.Config for Config. Invalid PEM in CABundle is ignored.
func (c Config) TLSConfig() *tls.Config {
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: c.InsecureSkipVerify, //nolint:gosec
	}

	if len(c.CABundle) > 0 {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		pool.AppendCertsFromPEM(c.CABundle)
		cfg.RootCAs = pool
	}

	return cfg
}
