package netx

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// NewHTTPClientFromSecret returns a client using the basic-auth or TLS
// credentials in creds.
func NewHTTPClientFromSecret(creds *corev1.Secret, caBundle []byte, insecureTLSVerify bool, timeout time.Duration) (*http.Client, error) {
	var (
		username  string
		password  string
		tlsConfig tls.Config
	)

	if creds != nil {
		switch creds.Type {
		case corev1.SecretTypeBasicAuth:
			username = string(creds.Data[corev1.BasicAuthUsernameKey])
			password = string(creds.Data[corev1.BasicAuthPasswordKey])
		case corev1.SecretTypeTLS:
			cert, err := tls.X509KeyPair(creds.Data[corev1.TLSCertKey], creds.Data[corev1.TLSPrivateKeyKey])
			if err != nil {
				return nil, err
			}
			tlsConfig.Certificates = append(tlsConfig.Certificates, cert)
		}
	}

	if len(caBundle) > 0 {
		cert, err := x509.ParseCertificate(caBundle)
		if err != nil {
			return nil, err
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		pool.AddCert(cert)
		tlsConfig.RootCAs = pool
	}

	transport := baseTransport()
	transport.TLSClientConfig = &tlsConfig
	transport.TLSClientConfig.InsecureSkipVerify = insecureTLSVerify

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
	if username != "" || password != "" {
		client.Transport = &basicRoundTripper{
			username: username,
			password: password,
			next:     client.Transport,
		}
	}

	return client, nil
}

type basicRoundTripper struct {
	username string
	password string
	next     http.RoundTripper
}

func (b *basicRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	request.SetBasicAuth(b.username, b.password)
	return b.next.RoundTrip(request)
}
