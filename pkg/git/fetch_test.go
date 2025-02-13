package git_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/internal/config"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/git"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newTestClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	return fake.NewClientBuilder().
		WithObjects(objs...).
		WithScheme(scheme).
		Build()
}

func newTestGithubServer(refs []string, TLSCfg *tls.Config) *httptest.Server {
	// fake response from github with capabilities
	header := "001e# service=git-upload-pack\n01552ada7cca738877df8459b3a34839a15e5683edaa HEAD\x00"
	header += "multi_ack thin-pack side-band side-band-64k ofs-delta shallow deepen-since deepen-not deepen-relative no-progress include-tag multi_ack_detailed allow-tip-sha1-in-want allow-reachable-sha1-in-want no-done symref=HEAD:refs/heads/master filter object-format=sha1 agent=git/github-f133c3a1d7e6\n"
	response := header
	for _, ref := range refs {
		response += ref + "\n"
	}
	response += "0000\n"

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v2/{$}", func(http.ResponseWriter, *http.Request) {
	})

	mux.HandleFunc("GET /info/refs", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, response)
	})

	ts := httptest.NewUnstartedServer(mux)
	if TLSCfg != nil {
		ts.TLS = TLSCfg
		ts.StartTLS()
	} else {
		ts.Start()
	}

	return ts
}

var _ = Describe("git fetch's LatestCommit tests", func() {
	When("secret credentials does not exist", func() {
		var (
			fakeGithub *httptest.Server
			refs       []string
		)

		BeforeEach(func() {
			refs = []string{
				"003f2ada7cca738877df8459b3a34839a15e5683edaa refs/heads/master",
				"004522a46b7cfd14db4c93c5fa1e27df1d6d7b6ef1da refs/heads/release/v0.5",
				"0044f1be9e1bd0387fb6ec0df35f38b147a7016937e6 refs/heads/test-simple",
				"003f56bca25f648a951c2f8fd6db4981e4a4f040ca4e refs/tags/example",
			}
		})

		It("returns the commit for the expected revision", func() {
			config.Set(&config.Config{
				GitClientTimeout: metav1.Duration{Duration: 0},
			})
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-ns",
				},
				Type: corev1.SecretTypeBasicAuth,
				Data: map[string][]byte{
					corev1.BasicAuthUsernameKey: []byte("username"),
					corev1.BasicAuthPasswordKey: []byte("password"),
				},
			}

			fakeGithub = newTestGithubServer(refs, nil)
			defer fakeGithub.Close()

			gr := &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-gitrepo",
					Namespace: "test-ns",
				},
				Spec: fleetv1.GitRepoSpec{
					ClientSecretName: "test-secret-different",
					Revision:         "example",
					Repo:             fakeGithub.URL,
				},
				Status: fleetv1.GitRepoStatus{
					Commit: "",
				},
			}

			c := newTestClient(secret)
			f := git.Fetch{}
			commit, err := f.LatestCommit(context.Background(), gr, c)
			Expect(err).ToNot(HaveOccurred())
			Expect(commit).To(Equal("56bca25f648a951c2f8fd6db4981e4a4f040ca4e"))
		})

		It("returns the commit for the expected branch", func() {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-ns",
				},
				Type: corev1.SecretTypeBasicAuth,
				Data: map[string][]byte{
					corev1.BasicAuthUsernameKey: []byte("username"),
					corev1.BasicAuthPasswordKey: []byte("password"),
				},
			}

			fakeGithub = newTestGithubServer(refs, nil)
			defer fakeGithub.Close()

			gr := &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-gitrepo",
					Namespace: "test-ns",
				},
				Spec: fleetv1.GitRepoSpec{
					ClientSecretName: "test-secret",
					Repo:             fakeGithub.URL,
					Branch:           "master",
				},
				Status: fleetv1.GitRepoStatus{
					Commit: "",
				},
			}

			c := newTestClient(secret)
			f := git.Fetch{}
			commit, err := f.LatestCommit(context.Background(), gr, c)
			Expect(err).ToNot(HaveOccurred())
			Expect(commit).To(Equal("2ada7cca738877df8459b3a34839a15e5683edaa"))
		})

		It("returns an error when secret's type is not expected", func() {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "test-ns",
				},
				Type: corev1.SecretTypeSSHAuth,
				Data: map[string][]byte{
					corev1.SSHAuthPrivateKey: []byte("Not_valid_key"),
					"known_hosts":            []byte("Not_valid_known_hosts"),
				},
			}

			fakeGithub = newTestGithubServer(refs, nil)
			defer fakeGithub.Close()

			gr := &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-gitrepo",
					Namespace: "test-ns",
				},
				Spec: fleetv1.GitRepoSpec{
					ClientSecretName: "test-secret",
					Repo:             fakeGithub.URL,
					Branch:           "master",
				},
				Status: fleetv1.GitRepoStatus{
					Commit: "",
				},
			}

			c := newTestClient(secret)
			f := git.Fetch{}
			commit, err := f.LatestCommit(context.Background(), gr, c)
			Expect(err).To(HaveOccurred())
			Expect(commit).To(BeEmpty())
			Expect(err.Error()).To(Equal("ssh: no key found"))
		})

		It("uses a Rancher CA bundle if configured", func() {
			cfg, ca, err := setupCerts()
			Expect(err).ToNot(HaveOccurred())

			buf := make([]byte, base64.StdEncoding.EncodedLen(len(ca)))
			base64.StdEncoding.Encode(buf, ca)

			fakeGithub = newTestGithubServer(refs, cfg)
			defer fakeGithub.Close()

			config.Set(&config.Config{
				GitClientTimeout: metav1.Duration{Duration: 0},
			})

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tls-ca-additional",
					Namespace: "cattle-system",
				},
				Data: map[string][]byte{
					"ca-additional.pem": ca,
				},
			}

			gr := &fleetv1.GitRepo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-gitrepo",
					Namespace: "test-ns",
				},
				Spec: fleetv1.GitRepoSpec{
					Repo:   fakeGithub.URL,
					Branch: "master",
				},
				Status: fleetv1.GitRepoStatus{
					Commit: "",
				},
			}

			c := newTestClient(secret)
			f := git.Fetch{}
			commit, err := f.LatestCommit(context.Background(), gr, c)
			Expect(err).ToNot(HaveOccurred())
			Expect(commit).To(Equal("2ada7cca738877df8459b3a34839a15e5683edaa"))

			// Try again without the secret, and check that fetching the latest commit fails
			c = newTestClient()
			commit, err = f.LatestCommit(context.Background(), gr, c)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("signed by unknown authority"))
			Expect(commit).To(BeEmpty())
		})
	})
})

// setupCerts creates a CA certificate, encodes it in PEM format, and creates server certificates signed with the
// previously generated CA cert.
// It returns server TLS config used to set up a test server, along with PEM data for the CA cert and an error, if any
// (in which case the other 2 returned values will be nil).
// Heavily inspired by https://shaneutt.com/blog/golang-ca-and-signed-cert-go/
func setupCerts() (serverTLSConf *tls.Config, caPEMData []byte, err error) {
	subject := pkix.Name{
		Organization:  []string{"Testing Fleet, Inc."},
		Country:       []string{"DE"},
		Locality:      []string{"Fleet City"},
		StreetAddress: []string{"Continuous Deployment Street"},
		PostalCode:    []string{"4242"},
	}

	// set up CA certificate
	ca := &x509.Certificate{
		SerialNumber:          big.NewInt(2025),
		Subject:               subject,
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	caPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, err
	}

	caBytes, err := x509.CreateCertificate(rand.Reader, ca, ca, &caPrivKey.PublicKey, caPrivKey)
	if err != nil {
		return nil, nil, err
	}

	caPEM := new(bytes.Buffer)
	_ = pem.Encode(caPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})

	// set up server certificate
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(2019),
		Subject:      subject,
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		SubjectKeyId: []byte{1, 2, 3, 4, 6},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}

	certPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, err
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, cert, ca, &certPrivKey.PublicKey, caPrivKey)
	if err != nil {
		return nil, nil, err
	}

	certPEM := new(bytes.Buffer)
	_ = pem.Encode(certPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})

	certPrivKeyPEM := new(bytes.Buffer)
	_ = pem.Encode(certPrivKeyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(certPrivKey),
	})

	serverCert, err := tls.X509KeyPair(certPEM.Bytes(), certPrivKeyPEM.Bytes())
	if err != nil {
		return nil, nil, err
	}

	serverTLSConf = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
	}

	return serverTLSConf, caPEM.Bytes(), nil
}
