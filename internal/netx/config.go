package netx

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
)

// TLSConfig holds TLS settings for outbound connections.
type TLSConfig struct {
	// CABundle holds PEM CAs trusted in addition to the system pool.
	CABundle           []byte
	InsecureSkipVerify bool
	// Certificates are client certificates presented for mutual TLS.
	Certificates []tls.Certificate
}

// TLSClientConfig returns a tls.Config built from c. It fails if CABundle is
// set but holds no valid PEM certificate.
func (c TLSConfig) TLSClientConfig() (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: c.InsecureSkipVerify, //nolint:gosec
		Certificates:       c.Certificates,
	}

	if len(c.CABundle) > 0 {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(c.CABundle) {
			return nil, errors.New("no valid certificates in CA bundle")
		}
		cfg.RootCAs = pool
	}

	return cfg, nil
}
