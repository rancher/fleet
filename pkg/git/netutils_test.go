package git_test

import (
	"encoding/pem"
	"net/http"
	"time"

	httpgit "github.com/go-git/go-git/v5/plumbing/transport/http"
	gossh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/pkg/git"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
)

const gitClientTimeout = time.Second * 30

var _ = Describe("git's GetAuthFromSecret tests", func() {
	var (
		secret *corev1.Secret
	)
	Context("Basic auth with no username nor password", func() {
		BeforeEach(func() {
			secret = &corev1.Secret{
				Type: corev1.SecretTypeBasicAuth,
			}
		})
		It("returns no error and no auth", func() {
			auth, err := git.GetAuthFromSecret("test-url.com", secret)
			Expect(err).ToNot(HaveOccurred())
			Expect(auth).To(BeNil())
		})

		It("returns no error and no auth when passing a nil secret", func() {
			auth, err := git.GetAuthFromSecret("test-url.com", nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(auth).To(BeNil())
		})
	})

	Context("Secret is not basic-auth nor ssh", func() {
		BeforeEach(func() {
			secret = &corev1.Secret{
				Type: corev1.SecretTypeTLS,
			}
		})

		It("returns no error and no auth", func() {
			auth, err := git.GetAuthFromSecret("test-url.com", secret)
			Expect(err).ToNot(HaveOccurred())
			Expect(auth).To(BeNil())
		})
	})

	Context("Basic auth with username and password", func() {
		var (
			username = []byte("username-test")
			password = []byte("1234issupersecure")
		)
		BeforeEach(func() {
			secret = &corev1.Secret{
				Type: corev1.SecretTypeBasicAuth,
				Data: map[string][]byte{
					corev1.BasicAuthUsernameKey: username,
					corev1.BasicAuthPasswordKey: password,
				},
			}
		})
		It("returns the basic auth Auth and no error", func() {
			auth, err := git.GetAuthFromSecret("test-url.com", secret)
			Expect(err).ToNot(HaveOccurred())
			Expect(auth).To(Equal(&httpgit.BasicAuth{
				Username: string(username),
				Password: string(password),
			}))
		})
	})
	Context("SSH auth with not valid git url", func() {
		BeforeEach(func() {
			secret = &corev1.Secret{
				Type: corev1.SecretTypeSSHAuth,
			}
		})
		It("returns an error and no auth", func() {
			auth, err := git.GetAuthFromSecret("notavalidurl", secret)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("failed to parse \"notavalidurl\""))
			Expect(auth).To(BeNil())
		})
	})

	Context("SSH auth with not valid ssh key", func() {
		BeforeEach(func() {
			secret = &corev1.Secret{
				Type: corev1.SecretTypeSSHAuth,
			}
		})
		It("returns an error and no auth", func() {
			auth, err := git.GetAuthFromSecret("git@github.com:rancher/fleet.git", secret)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("ssh: no key found"))
			Expect(auth).To(BeNil())
		})
	})

	Context("SSH auth with valid private key and no known_hosts", func() {
		var privateKey = []byte(`-----BEGIN RSA PRIVATE KEY-----
MIIEogIBAAKCAQEAmVh/5bCTwmFU+F7OWyYT6JFkG8V06AdesKSMyeJwT4kGs3Pm
vKEzKd/CExhd25Tzk5CD8jj6x9usZOtnI0rmCJEgkviWbk6b0K0jPs2b4a6fSbvE
GpSYheS89cQ7m8YrQr6MuqstjpS1Yz/uWwN0DCrNupyf0GkesqKLlgElPuwcfeQo
OmyLhY2xViK2ctLzricbKqDMqCFd1WdaYEut6lh3z/Gy/tPk/CJVkP1VGC+KTaer
vyRKnH9By1VsXUOzOT1NVFjOJfXIqtyTwnE+d24WR9mPcw7kPReiXyS6DoUfcmiq
4irqaN1smp52iX7EbWsIhir74TrBP51q1M+QFQIDAQABAoIBACTmMNB6bvPFLAcf
+RPh08SQx8APAZSbwWNMFTy3KkNZO62O5CTbvU4EM9UYde1SqFIH4lg08dOJvrAC
HS1W5oeFNItpGfmtHL1YDDUekLX7qQS7E/M5coI1imqxL47KXrqO05pPeoTmr8cU
KSzpZdFPs3WGHsatpN9jUadk2yuKkVEjWxMLOtnvbxuhPSuJmhJ5oajCHhGcGD1k
RMBsPrvRy9J5nEEfYA+KKpWDhIiS6fP5n0YvroIpcJVU5Fo/EZClNrEBkVl0bOms
d103PWADAputJrTYpv1b/YsT3STM698DhXsTtvK3RvXouJA+P+JVIO5wnH3ySDhL
L73FgcECgYEA3ee6oNybgMiL9dXAo4lD2BbKU/fbgXZXZLlxrex1x08u9EjOf2+h
R7ZvJnmwbTtpwis0mCbkvS6SrMO4qmlTzF0KvWWu6dobBpDUIVBU0w4No9J4c7BS
lYEGFzJH9VMugZMmstT0Yn9ugntShQjf+gQp6RSr4odssNFqqiOCB8kCgYEAsOgY
5wHEvR+7v+6iaO+/ZGFgBcmt+B3aC+pwjsyVVpUuAAA3Dyb8T5CIMi44Ys/1C3TC
b/cypuLFzgdccjqHdZ7z4Tmwo5skUmHTtEsb01mG2FxBBm4a81+FwoN+QRnZDpj6
JQIfWWmbsP56XPOaqlMYfIhLqBkNwtuvhY4fA+0CgYBwrKJh3cKD0NDoYcHwB9nQ
FjpkCn2Frg5QEa18T426RyWjWnin0onE/QhRNAb2X+2ibwfEnjMVMFm/qZ3RwauQ
IEo8wy3ehiWk3tMnmz+G7yLT5SHONGCqkxoBm0FYewUpPAuxUFpKzUPSs0XCUTBR
Jd4WAK4KVxNEcQFFJMR4qQKBgEc8TrrG1YgqfRneZ/vFftZW96mc+rbMnn7p2oVG
EGSbEbjiXUl2s2b+ljlOr1nqz4vbamhXrEfTTT+Xazx8IQvWA/KPnndjA49A4VTa
YcwLYudAztZeA/A4aM5Y0MA6PlNIeoHohuMkSZNOBcvkNEWdzGBpKb34yLfMarNm
9UpJAoGAU4sGLwlAHGo+PXUi0PyLtQcxcbytkLAQsKMpuZDvNT/KAmu0kJ0p9InN
5VKnu9SpmXPxjinS8Mg9QXLrfi5SArEllzfXrgW9OU7ht2xandDD+B8S1cmZF+Yz
1salKM9mBBkl0sWraqtzQSEDjPeAz8P4TpQKn6kIMiZkMnrurvI=
-----END RSA PRIVATE KEY-----`)
		BeforeEach(func() {
			secret = &corev1.Secret{
				Type: corev1.SecretTypeSSHAuth,
				Data: map[string][]byte{
					corev1.SSHAuthPrivateKey: privateKey,
				},
			}
		})
		It("returns no error and the ssh auth", func() {
			auth, err := git.GetAuthFromSecret("git@github.com:rancher/fleet.git", secret)
			Expect(err).ToNot(HaveOccurred())
			expectedSigner, err := ssh.ParsePrivateKey(privateKey)
			Expect(err).ToNot(HaveOccurred())
			pk, ok := auth.(*gossh.PublicKeys)
			Expect(ok).To(BeTrue())
			Expect(pk.User).To(Equal("git"))
			Expect(pk.Signer).To(Equal(expectedSigner))
		})
	})

	Context("SSH auth with valid private key and known_hosts", func() {
		var (
			privateKey = []byte(`-----BEGIN RSA PRIVATE KEY-----
MIIEogIBAAKCAQEAmVh/5bCTwmFU+F7OWyYT6JFkG8V06AdesKSMyeJwT4kGs3Pm
vKEzKd/CExhd25Tzk5CD8jj6x9usZOtnI0rmCJEgkviWbk6b0K0jPs2b4a6fSbvE
GpSYheS89cQ7m8YrQr6MuqstjpS1Yz/uWwN0DCrNupyf0GkesqKLlgElPuwcfeQo
OmyLhY2xViK2ctLzricbKqDMqCFd1WdaYEut6lh3z/Gy/tPk/CJVkP1VGC+KTaer
vyRKnH9By1VsXUOzOT1NVFjOJfXIqtyTwnE+d24WR9mPcw7kPReiXyS6DoUfcmiq
4irqaN1smp52iX7EbWsIhir74TrBP51q1M+QFQIDAQABAoIBACTmMNB6bvPFLAcf
+RPh08SQx8APAZSbwWNMFTy3KkNZO62O5CTbvU4EM9UYde1SqFIH4lg08dOJvrAC
HS1W5oeFNItpGfmtHL1YDDUekLX7qQS7E/M5coI1imqxL47KXrqO05pPeoTmr8cU
KSzpZdFPs3WGHsatpN9jUadk2yuKkVEjWxMLOtnvbxuhPSuJmhJ5oajCHhGcGD1k
RMBsPrvRy9J5nEEfYA+KKpWDhIiS6fP5n0YvroIpcJVU5Fo/EZClNrEBkVl0bOms
d103PWADAputJrTYpv1b/YsT3STM698DhXsTtvK3RvXouJA+P+JVIO5wnH3ySDhL
L73FgcECgYEA3ee6oNybgMiL9dXAo4lD2BbKU/fbgXZXZLlxrex1x08u9EjOf2+h
R7ZvJnmwbTtpwis0mCbkvS6SrMO4qmlTzF0KvWWu6dobBpDUIVBU0w4No9J4c7BS
lYEGFzJH9VMugZMmstT0Yn9ugntShQjf+gQp6RSr4odssNFqqiOCB8kCgYEAsOgY
5wHEvR+7v+6iaO+/ZGFgBcmt+B3aC+pwjsyVVpUuAAA3Dyb8T5CIMi44Ys/1C3TC
b/cypuLFzgdccjqHdZ7z4Tmwo5skUmHTtEsb01mG2FxBBm4a81+FwoN+QRnZDpj6
JQIfWWmbsP56XPOaqlMYfIhLqBkNwtuvhY4fA+0CgYBwrKJh3cKD0NDoYcHwB9nQ
FjpkCn2Frg5QEa18T426RyWjWnin0onE/QhRNAb2X+2ibwfEnjMVMFm/qZ3RwauQ
IEo8wy3ehiWk3tMnmz+G7yLT5SHONGCqkxoBm0FYewUpPAuxUFpKzUPSs0XCUTBR
Jd4WAK4KVxNEcQFFJMR4qQKBgEc8TrrG1YgqfRneZ/vFftZW96mc+rbMnn7p2oVG
EGSbEbjiXUl2s2b+ljlOr1nqz4vbamhXrEfTTT+Xazx8IQvWA/KPnndjA49A4VTa
YcwLYudAztZeA/A4aM5Y0MA6PlNIeoHohuMkSZNOBcvkNEWdzGBpKb34yLfMarNm
9UpJAoGAU4sGLwlAHGo+PXUi0PyLtQcxcbytkLAQsKMpuZDvNT/KAmu0kJ0p9InN
5VKnu9SpmXPxjinS8Mg9QXLrfi5SArEllzfXrgW9OU7ht2xandDD+B8S1cmZF+Yz
1salKM9mBBkl0sWraqtzQSEDjPeAz8P4TpQKn6kIMiZkMnrurvI=
-----END RSA PRIVATE KEY-----`)
			fingerPrint = "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBOLWGeeq/e1mK/zH47UeQeMtdh+NEz6j7xp5cAINcV2pPWgAsuyh5dumMv1RkC1rr0pmWekCoMnR2c4+PllRqrQ="
		)
		BeforeEach(func() {
			secret = &corev1.Secret{
				Type: corev1.SecretTypeSSHAuth,
				Data: map[string][]byte{
					corev1.SSHAuthPrivateKey: privateKey,
					"known_hosts":            []byte("[localhost]: " + fingerPrint),
				},
			}
		})
		It("returns no error and the ssh auth", func() {
			auth, err := git.GetAuthFromSecret("git@github.com:rancher/fleet.git", secret)
			Expect(err).ToNot(HaveOccurred())
			expectedSigner, err := ssh.ParsePrivateKey(privateKey)
			Expect(err).ToNot(HaveOccurred())
			pk, ok := auth.(*gossh.PublicKeys)
			Expect(ok).To(BeTrue())
			Expect(pk.User).To(Equal("git"))
			Expect(pk.Signer).To(Equal(expectedSigner))
		})
	})

	Context("SSH auth with valid private key and invalid known_hosts", func() {
		var (
			privateKey = []byte(`-----BEGIN RSA PRIVATE KEY-----
MIIEogIBAAKCAQEAmVh/5bCTwmFU+F7OWyYT6JFkG8V06AdesKSMyeJwT4kGs3Pm
vKEzKd/CExhd25Tzk5CD8jj6x9usZOtnI0rmCJEgkviWbk6b0K0jPs2b4a6fSbvE
GpSYheS89cQ7m8YrQr6MuqstjpS1Yz/uWwN0DCrNupyf0GkesqKLlgElPuwcfeQo
OmyLhY2xViK2ctLzricbKqDMqCFd1WdaYEut6lh3z/Gy/tPk/CJVkP1VGC+KTaer
vyRKnH9By1VsXUOzOT1NVFjOJfXIqtyTwnE+d24WR9mPcw7kPReiXyS6DoUfcmiq
4irqaN1smp52iX7EbWsIhir74TrBP51q1M+QFQIDAQABAoIBACTmMNB6bvPFLAcf
+RPh08SQx8APAZSbwWNMFTy3KkNZO62O5CTbvU4EM9UYde1SqFIH4lg08dOJvrAC
HS1W5oeFNItpGfmtHL1YDDUekLX7qQS7E/M5coI1imqxL47KXrqO05pPeoTmr8cU
KSzpZdFPs3WGHsatpN9jUadk2yuKkVEjWxMLOtnvbxuhPSuJmhJ5oajCHhGcGD1k
RMBsPrvRy9J5nEEfYA+KKpWDhIiS6fP5n0YvroIpcJVU5Fo/EZClNrEBkVl0bOms
d103PWADAputJrTYpv1b/YsT3STM698DhXsTtvK3RvXouJA+P+JVIO5wnH3ySDhL
L73FgcECgYEA3ee6oNybgMiL9dXAo4lD2BbKU/fbgXZXZLlxrex1x08u9EjOf2+h
R7ZvJnmwbTtpwis0mCbkvS6SrMO4qmlTzF0KvWWu6dobBpDUIVBU0w4No9J4c7BS
lYEGFzJH9VMugZMmstT0Yn9ugntShQjf+gQp6RSr4odssNFqqiOCB8kCgYEAsOgY
5wHEvR+7v+6iaO+/ZGFgBcmt+B3aC+pwjsyVVpUuAAA3Dyb8T5CIMi44Ys/1C3TC
b/cypuLFzgdccjqHdZ7z4Tmwo5skUmHTtEsb01mG2FxBBm4a81+FwoN+QRnZDpj6
JQIfWWmbsP56XPOaqlMYfIhLqBkNwtuvhY4fA+0CgYBwrKJh3cKD0NDoYcHwB9nQ
FjpkCn2Frg5QEa18T426RyWjWnin0onE/QhRNAb2X+2ibwfEnjMVMFm/qZ3RwauQ
IEo8wy3ehiWk3tMnmz+G7yLT5SHONGCqkxoBm0FYewUpPAuxUFpKzUPSs0XCUTBR
Jd4WAK4KVxNEcQFFJMR4qQKBgEc8TrrG1YgqfRneZ/vFftZW96mc+rbMnn7p2oVG
EGSbEbjiXUl2s2b+ljlOr1nqz4vbamhXrEfTTT+Xazx8IQvWA/KPnndjA49A4VTa
YcwLYudAztZeA/A4aM5Y0MA6PlNIeoHohuMkSZNOBcvkNEWdzGBpKb34yLfMarNm
9UpJAoGAU4sGLwlAHGo+PXUi0PyLtQcxcbytkLAQsKMpuZDvNT/KAmu0kJ0p9InN
5VKnu9SpmXPxjinS8Mg9QXLrfi5SArEllzfXrgW9OU7ht2xandDD+B8S1cmZF+Yz
1salKM9mBBkl0sWraqtzQSEDjPeAz8P4TpQKn6kIMiZkMnrurvI=
-----END RSA PRIVATE KEY-----`)
		)
		BeforeEach(func() {
			secret = &corev1.Secret{
				Type: corev1.SecretTypeSSHAuth,
				Data: map[string][]byte{
					corev1.SSHAuthPrivateKey: privateKey,
					"known_hosts":            []byte("Not_valid_known_hosts"),
				},
			}
		})
		It("returns an error and no auth", func() {
			auth, err := git.GetAuthFromSecret("git@github.com:rancher/fleet.git", secret)
			Expect(err).To(HaveOccurred())
			Expect(auth).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("knownhosts: missing host pattern"))
		})
	})
})

var _ = Describe("git's GetHTTPClientFromSecret tests", func() {
	When("using a nil secret, no caBudle and InsecureSkipVerify = false", func() {
		var caBundle []byte
		client, err := git.GetHTTPClientFromSecret(nil, caBundle, false, gitClientTimeout)
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())
		expectedTransport, ok := client.Transport.(*http.Transport)
		Expect(ok).To(BeTrue())

		It("returns a client's transport with InsecureSkipVerify = false", func() {
			Expect(expectedTransport.TLSClientConfig.InsecureSkipVerify).To(BeFalse())
		})

		It("returns a client's transport with no credentials", func() {
			// no auth is set by the transport RoundTrip
			req := &http.Request{}
			_, _ = client.Transport.RoundTrip(req)
			auth := req.Header.Get("Authorization")
			Expect(auth).To(BeEmpty())
		})

		It("returns a client's transport no rootCAs", func() {
			// no auth is set by the transport RoundTrip
			Expect(expectedTransport.TLSClientConfig.RootCAs).To(BeNil())
		})
	})

	When("using a nil secret, no caBudle and InsecureSkipVerify = true", func() {
		var caBundle []byte
		client, err := git.GetHTTPClientFromSecret(nil, caBundle, true, gitClientTimeout)
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())
		expectedTransport, ok := client.Transport.(*http.Transport)
		Expect(ok).To(BeTrue())

		It("returns a client's transport with InsecureSkipVerify = true", func() {
			Expect(expectedTransport.TLSClientConfig.InsecureSkipVerify).To(BeTrue())
		})

		It("returns a client's transport with no credentials", func() {
			// no auth is set by the transport RoundTrip
			req := &http.Request{}
			_, _ = client.Transport.RoundTrip(req)
			auth := req.Header.Get("Authorization")
			Expect(auth).To(BeEmpty())
		})

		It("returns a client's transport no rootCAs", func() {
			// no auth is set by the transport RoundTrip
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

		block, _ := pem.Decode([]byte(caBundlePEM))
		Expect(block).ToNot(BeNil())
		client, err := git.GetHTTPClientFromSecret(nil, block.Bytes, true, gitClientTimeout)
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())
		expectedTransport, ok := client.Transport.(*http.Transport)
		Expect(ok).To(BeTrue())

		It("returns a client's transport with InsecureSkipVerify = true", func() {
			Expect(expectedTransport.TLSClientConfig.InsecureSkipVerify).To(BeTrue())
		})

		It("returns a client's transport with no credentials", func() {
			// no auth is set by the transport RoundTrip
			req := &http.Request{}
			_, _ = client.Transport.RoundTrip(req)
			auth := req.Header.Get("Authorization")
			Expect(auth).To(BeEmpty())
		})

		It("returns a client's transport no rootCAs", func() {
			// no auth is set by the transport RoundTrip
			Expect(expectedTransport.TLSClientConfig.RootCAs).ToNot(BeNil())
		})
	})

	When("using a malformed ca bundle", func() {
		caBundle := []byte(`-----BEGIN CERTIFICATE-----
SUPER FAKE CERT
-----END CERTIFICATE-----`)
		client, err := git.GetHTTPClientFromSecret(nil, caBundle, true, gitClientTimeout)
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
		client, err := git.GetHTTPClientFromSecret(secret, nil, false, gitClientTimeout)
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())

		It("returns a client's transport roundtrip that sets credentials", func() {
			// no auth is set by the transport RoundTrip
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
		client, err := git.GetHTTPClientFromSecret(secret, nil, false, gitClientTimeout)
		Expect(err).ToNot(HaveOccurred())
		Expect(client).ToNot(BeNil())

		It("returns a client's transport with certificates", func() {
			expectedTransport, ok := client.Transport.(*http.Transport)
			Expect(ok).To(BeTrue())
			Expect(len(expectedTransport.TLSClientConfig.Certificates)).ToNot(BeZero())
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
		_, err := git.GetHTTPClientFromSecret(secret, nil, false, gitClientTimeout)

		It("returns an error", func() {
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("tls: failed to find any PEM data in certificate input"))
		})
	})
})
