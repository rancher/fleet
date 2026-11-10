package netx

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"

	"sigs.k8s.io/controller-runtime/pkg/log"
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

// WithProxyCA returns a copy of c with the PEM certificates from
// PROXY_CA_BUNDLE appended to CABundle. An invalid PROXY_CA_BUNDLE is logged
// and ignored. The caller's CABundle slice is never modified.
func (c TLSConfig) WithProxyCA() TLSConfig {
	c.CABundle = append([]byte(nil), c.CABundle...)
	if proxyCAPEM, ok := os.LookupEnv(ProxyCABundleEnvVar); ok && proxyCAPEM != "" {
		proxyBytes := []byte(proxyCAPEM)
		tmpPool := x509.NewCertPool()
		if !tmpPool.AppendCertsFromPEM(proxyBytes) {
			log.Log.Info(ProxyCABundleEnvVar + " is set but contains no valid PEM certificates; ignoring proxy CA bundle")
		} else {
			if len(c.CABundle) > 0 && c.CABundle[len(c.CABundle)-1] != '\n' {
				c.CABundle = append(c.CABundle, '\n')
			}
			c.CABundle = append(c.CABundle, proxyBytes...)
		}
	}
	return c
}
