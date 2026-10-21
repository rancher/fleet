package netx_test

import (
	"encoding/pem"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"

	"github.com/rancher/fleet/pkg/netx"
)

const clientTimeout = time.Second * 30

var _ = Describe("NewHTTPClientFromSecret", func() {
	When("using a nil secret, no caBudle and InsecureSkipVerify = false", func() {
		var caBundle []byte
		client, err := netx.NewHTTPClientFromSecret(nil, caBundle, false, clientTimeout)
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())
		expectedTransport, ok := client.Transport.(*http.Transport)
		Expect(ok).To(BeTrue())

		It("returns a client's transport with InsecureSkipVerify = false", func() {
			Expect(expectedTransport.TLSClientConfig.InsecureSkipVerify).To(BeFalse())
		})

		It("returns a client's transport with no credentials", func() {
			
			req := &http.Request{}
			_, _ = client.Transport.RoundTrip(req)
			auth := req.Header.Get("Authorization")
			Expect(auth).To(BeEmpty())
		})

		It("returns a client's transport no rootCAs", func() {
			Expect(expectedTransport.TLSClientConfig.RootCAs).To(BeNil())
		})
	})

	When("using a nil secret, no caBudle and InsecureSkipVerify = true", func() {
		var caBundle []byte
		client, err := netx.NewHTTPClientFromSecret(nil, caBundle, true, clientTimeout)
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())
		expectedTransport, ok := client.Transport.(*http.Transport)
		Expect(ok).To(BeTrue())

		It("returns a client's transport with InsecureSkipVerify = true", func() {
			Expect(expectedTransport.TLSClientConfig.InsecureSkipVerify).To(BeTrue())
		})

		It("returns a client's transport with no credentials", func() {
			req := &http.Request{}
			_, _ = client.Transport.RoundTrip(req)
			auth := req.Header.Get("Authorization")
			Expect(auth).To(BeEmpty())
		})

		It("returns a client's transport no rootCAs", func() {
			Expect(expectedTransport.TLSClientConfig.RootCAs).To(BeNil())
		})
	})

	When("using a nil secret, caBudle and InsecureSkipVerify = true", func() {
		caBundlePEM := []byte(`-----BEGIN CERTIFICATE-----
MIICGTCCAZ+gAwIBAgIQCeCTZaz32ci5PhwLBCou8zAKBggqhkjOPQQDAzBOMQsw
CQYDVQQGEwJVUzEXMBUGA1UEChMORGlnaUNlcnQsIEluYy4xJjAkBgNVBAMTHURp
Z2lDZXJ0IFRMUyBFQ0MgUDM4NCBSb290IEc1MB4XDTIxMDExNTAwMDAwMFoXDTQ2
MDExNDIzNTk1OVowTjELMAkGA1UEBhMCVVMxFzAVBgNVBAoTDkRpZ2lDZXJ0LCBJ
bmMuMSYwJAYDVQQDEx1EaWdpQ2VydCBUTFMgRUNDIFAzODQgUm9vdCBHNTB2MBAG
ByqGSM49AgEGBSuBBAAiA2IABMFEoc8Rl1Ca3iOCNQfN0MsYndLxf3c1TzvdlHJS
7cI7+Oz6e2tYIOyZrsn8aLN1udsJ7MgT9U7GCh1mMEy7H0cKPGEQQil8pQgO4CLp
0zVozptjn4S1mU1YoI71VOeVyaNCMEAwHQYDVR0OBBYEFMFRRVBZqz7nLFr6ICIS
B4CIfBFqMA4GA1UdDwEB/wQEAwIBhjAPBgNVHRMBAf8EBTADAQH/MAoGCCqGSM49
BAMDA2gAMGUCMQCJao1H5+z8blUD2WdsJk6Dxv3J+ysTvLd6jLRl0mlpYxNjOyZQ
LgGheQaRnUi/wr4CMEfDFXuxoJGZSZOoPHzoRgaLLPIxAJSdYsiJvRmEFOml+wG4
DXZDjC5Ty3zfDBeWUA==
-----END CERTIFICATE-----`)

		block, _ := pem.Decode(caBundlePEM)
		Expect(block).ToNot(BeNil())
		client, err := netx.NewHTTPClientFromSecret(nil, block.Bytes, true, clientTimeout)
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())
		expectedTransport, ok := client.Transport.(*http.Transport)
		Expect(ok).To(BeTrue())

		It("returns a client's transport with InsecureSkipVerify = true", func() {
			Expect(expectedTransport.TLSClientConfig.InsecureSkipVerify).To(BeTrue())
		})

		It("returns a client's transport with no credentials", func() {
			req := &http.Request{}
			_, _ = client.Transport.RoundTrip(req)
			auth := req.Header.Get("Authorization")
			Expect(auth).To(BeEmpty())
		})

		It("returns a client's transport no rootCAs", func() {
			Expect(expectedTransport.TLSClientConfig.RootCAs).ToNot(BeNil())
		})
	})

	When("using a malformed ca bundle", func() {
		caBundle := []byte(`-----BEGIN CERTIFICATE-----
SUPER FAKE CERT
-----END CERTIFICATE-----`)
		client, err := netx.NewHTTPClientFromSecret(nil, caBundle, true, clientTimeout)
		It("returns an error", func() {
			Expect(err).To(HaveOccurred())
			Expect(client).To(BeNil())
			Expect(err.Error()).To(Equal("x509: malformed certificate"))
		})
	})

	When("using a valid basic auth secret", func() {
		username := []byte("superuser")
		password := []byte("supersecure")
		secret := &corev1.Secret{
			Type: corev1.SecretTypeBasicAuth,
			Data: map[string][]byte{
				corev1.BasicAuthUsernameKey: username,
				corev1.BasicAuthPasswordKey: password,
			},
		}
		client, err := netx.NewHTTPClientFromSecret(secret, nil, false, clientTimeout)
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())

		It("returns a client's transport roundtrip that sets credentials", func() {
			req := &http.Request{}
			req.Header = make(map[string][]string)
			_, _ = client.Transport.RoundTrip(req)
			auth := req.Header.Get("Authorization")
			Expect(auth).To(Equal("Basic c3VwZXJ1c2VyOnN1cGVyc2VjdXJl"))
		})
	})

	When("using a valid tls secret", func() {
		const certificatePEM = `
-----BEGIN CERTIFICATE-----
MIIBLjCB4aADAgECAhAX0YGTviqMISAQJRXoNCNPMAUGAytlcDASMRAwDgYDVQQK
EwdBY21lIENvMB4XDTE5MDUxNjIxNTQyNloXDTIwMDUxNTIxNTQyNlowEjEQMA4G
A1UEChMHQWNtZSBDbzAqMAUGAytlcAMhAAvgtWC14nkwPb7jHuBQsQTIbcd4bGkv
xRStmmNveRKRo00wSzAOBgNVHQ8BAf8EBAMCBaAwEwYDVR0lBAwwCgYIKwYBBQUH
AwIwDAYDVR0TAQH/BAIwADAWBgNVHREEDzANggtleGFtcGxlLmNvbTAFBgMrZXAD
QQD8GRcqlKUx+inILn9boF2KTjRAOdazENwZ/qAicbP1j6FYDc308YUkv+Y9FN/f
7Q7hF9gRomDQijcjKsJGqjoI
-----END CERTIFICATE-----`

		var keyPEM = `
-----BEGIN PRIVATE KEY-----
MC4CAQAwBQYDK2VwBCIEINifzf07d9qx3d44e0FSbV4mC/xQxT644RRbpgNpin7I
-----END PRIVATE KEY-----`
		secret := &corev1.Secret{
			Type: corev1.SecretTypeTLS,
			Data: map[string][]byte{
				corev1.TLSCertKey:       []byte(certificatePEM),
				corev1.TLSPrivateKeyKey: []byte(keyPEM),
			},
		}
		client, err := netx.NewHTTPClientFromSecret(secret, nil, false, clientTimeout)
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())

		It("returns a client's transport with certificates", func() {
			expectedTransport, ok := client.Transport.(*http.Transport)
			Expect(ok).To(BeTrue())
			Expect(expectedTransport.TLSClientConfig.Certificates).ToNot(BeEmpty())
		})
	})

	When("using a non valid tls secret", func() {
		const certificatePEM = `
-----BEGIN CERTIFICATE-----
THIS IS NOT A VALID CERTIFICATE
-----END CERTIFICATE-----`

		var keyPEM = `
-----BEGIN PRIVATE KEY-----
THIS IS NOT A VALID KEY
-----END PRIVATE KEY-----`
		secret := &corev1.Secret{
			Type: corev1.SecretTypeTLS,
			Data: map[string][]byte{
				corev1.TLSCertKey:       []byte(certificatePEM),
				corev1.TLSPrivateKeyKey: []byte(keyPEM),
			},
		}
		_, err := netx.NewHTTPClientFromSecret(secret, nil, false, clientTimeout)

		It("returns an error", func() {
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("tls: failed to find any PEM data in certificate input"))
		})
	})
})
