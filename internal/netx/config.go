package netx

import (
	"crypto/tls"
	"crypto/x509"
)

// TLSConfig holds TLS settings for outbound connections.
type TLSConfig struct {
	// CABundle holds PEM CAs trusted in addition to the system pool.
	CABundle           []byte
	InsecureSkipVerify bool
}

// TLSClientConfig returns a tls.Config built from c. Invalid PEM in CABundle is ignored.
func (c TLSConfig) TLSClientConfig() *tls.Config {
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
