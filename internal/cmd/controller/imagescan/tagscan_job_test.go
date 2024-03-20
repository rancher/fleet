package imagescan

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetv1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v2/pkg/genericcondition"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func TestLatestTag(t *testing.T) {
	var alphabeticalVersions = []string{"a", "b", "c"}

	tests := []struct {
		name, want string
		policy     fleetv1.ImagePolicyChoice
	}{
		{
			name: "alphabetical asc lowercase",
			policy: fleetv1.ImagePolicyChoice{
				Alphabetical: &fleetv1.AlphabeticalPolicy{Order: "asc"},
			},
			want: "a",
		},
		{
			name: "alphabetical asc uppercase",
			policy: fleetv1.ImagePolicyChoice{
				Alphabetical: &fleetv1.AlphabeticalPolicy{Order: "ASC"},
			},
			want: "a",
		},
		{
			name: "alphabetical desc lowercase",
			policy: fleetv1.ImagePolicyChoice{
				Alphabetical: &fleetv1.AlphabeticalPolicy{Order: "desc"},
			},
			want: "c",
		},
		{
			name: "alphabetical desc uppercase",
			policy: fleetv1.ImagePolicyChoice{
				Alphabetical: &fleetv1.AlphabeticalPolicy{Order: "DESC"},
			},
			want: "c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := latestTag(tt.policy, alphabeticalVersions)
			if err != nil {
				t.Fatalf("Error calling latestTag: %v", err)
			}

			if got != tt.want {
				t.Errorf("latestTag() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTagScanJob_Execute_with_public_repo(t *testing.T) {
	// This test hits the "redis" repository in Docker to grab an image.
	// It could be down. The actual registry it hits isn't significant.
	scan := newTestImageScan("redis")
	fc := newTestClient(scan)
	ctx := context.TODO()

	job := NewTagScanJob(fc, scan.Namespace, scan.Name)
	if err := job.Execute(ctx); err != nil {
		t.Fatal(err)
	}

	if err := fc.Get(ctx, client.ObjectKeyFromObject(scan), scan); err != nil {
		t.Fatal(err)
	}

	if scan.Status.LatestTag == "" {
		t.Error("did not get a latest tag")
	}
}

func TestTagScanJob_Execute_with_CA_bundle(t *testing.T) {
	serverTLS, caPEM := newTestCAAndCert(t)
	dockerServer := newTestDockerServer(t, serverTLS, "redis", []string{"7.1.0", "7.2.4", "latest"})
	configMap := newTestConfigMap(map[string]string{
		"ca.crt": caPEM,
	})

	scan := newTestImageScan(imageServerURL(dockerServer.URL)+"/redis", func(f *fleetv1.ImageScan) {
		f.Spec.CABundleRef = &corev1.LocalObjectReference{
			Name: configMap.Name,
		}
	})
	fc := newTestClient(scan, configMap)
	ctx := context.TODO()

	job := NewTagScanJob(fc, scan.Namespace, scan.Name)
	if err := job.Execute(ctx); err != nil {
		t.Fatal(err)
	}

	if err := fc.Get(ctx, client.ObjectKeyFromObject(scan), scan); err != nil {
		t.Fatal(err)
	}

	if scan.Status.LatestTag != "7.2.4" {
		t.Errorf("got .Status.LatestTag %v, want %v", scan.Status.LatestTag, "7.2.4")
	}
}

func TestTagScanJob_Execute_with_no_CA_bundle(t *testing.T) {
	serverTLS, _ := newTestCAAndCert(t)
	dockerServer := newTestDockerServer(t, serverTLS, "redis", []string{"7.1.0", "7.2.4", "latest"})

	scan := newTestImageScan(imageServerURL(dockerServer.URL) + "/redis")
	fc := newTestClient(scan)

	ml := &mockLogger{}

	ctx := log.IntoContext(context.TODO(), logr.New(ml))
	job := NewTagScanJob(fc, scan.Namespace, scan.Name)

	if err := job.Execute(ctx); err != nil {
		t.Fatal(err)
	}

	err := fc.Get(ctx, client.ObjectKeyFromObject(scan), scan)
	if err != nil {
		t.Fatal(err)
	}

	assertConditionMessage(t, scan.Status.Conditions, fleet.ImageScanScanCondition, "tls: failed to verify certificate")
	ml.assertLogged(t, "error: Failed to list remote tags.*x509: certificate signed by unknown authority")
}

func TestTagScanJob_Execute_with_missing_secret(t *testing.T) {
	serverTLS, _ := newTestCAAndCert(t)
	dockerServer := newTestDockerServer(t, serverTLS, "redis", []string{"7.1.0", "7.2.4", "latest"})
	scan := newTestImageScan(imageServerURL(dockerServer.URL)+"/redis", func(f *fleetv1.ImageScan) {
		f.Spec.CABundleRef = &corev1.LocalObjectReference{
			Name: "test-configmap",
		}
	})
	fc := newTestClient(scan)
	ml := &mockLogger{}
	ctx := log.IntoContext(context.TODO(), logr.New(ml))
	job := NewTagScanJob(fc, scan.Namespace, scan.Name)
	if err := job.Execute(ctx); err != nil {
		t.Fatal(err)
	}

	if err := fc.Get(ctx, client.ObjectKeyFromObject(scan), scan); err != nil {
		t.Fatal(err)
	}

	assertConditionMessage(t, scan.Status.Conditions, fleet.ImageScanScanCondition, `configmaps "test-configmap" not found`)
	ml.assertLogged(t, "error: Failed to get referenced CA bundle")
}

func imageServerURL(s string) string {
	return strings.TrimPrefix(s, "https://")
}

func newTestClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(fleetv1.AddToScheme(scheme))

	return fake.NewClientBuilder().
		WithObjects(objs...).
		WithScheme(scheme).
		WithStatusSubresource(&fleetv1.ImageScan{}).
		Build()
}

func newTestConfigMap(data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-config-map",
			Namespace: "test-ns",
		},
		Data: data,
	}
}

func newTestImageScan(imageURL string, opts ...func(*fleetv1.ImageScan)) *fleetv1.ImageScan {
	is := &fleetv1.ImageScan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-scan",
			Namespace: "test-ns",
		},
		Spec: fleetv1.ImageScanSpec{
			Image: imageURL,
		},
	}

	for _, opt := range opts {
		opt(is)
	}

	return is
}

// builds a fake docker server with the provided tls.Config.
func newTestDockerServer(t *testing.T, tlsConfig *tls.Config, name string, tags []string) *httptest.Server {
	tagsResponse := map[string]any{
		"name": name,
		"tags": tags,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v2/{$}", func(http.ResponseWriter, *http.Request) {
	})

	mux.HandleFunc(fmt.Sprintf("GET /v2/%s/tags/list", name), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		if err := enc.Encode(tagsResponse); err != nil {
			t.Errorf("failed to encode tags HTTP response: %s", err)
		}
	})

	mux.HandleFunc(fmt.Sprintf("GET /v2/%s/manifests/{version}", name), func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		if err := enc.Encode(map[string]any{
			"name": name,
			"tag":  r.PathValue("version"),
		}); err != nil {
			t.Errorf("failed to encode manifest HTTP response: %s", err)
		}
	})

	ts := httptest.NewUnstartedServer(mux)
	ts.TLS = tlsConfig
	ts.StartTLS()

	t.Cleanup(func() {
		ts.Close()
	})

	return ts
}

// returns a tls.Config for configuring the server and the CA certificate.
func newTestCAAndCert(t *testing.T) (*tls.Config, string) {
	t.Helper()
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "test",
			Organization: []string{"Σ Acme Co"},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().AddDate(10, 0, 0),
		IsCA:      true,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth,
		},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	caPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		t.Fatal(err)
	}

	caBytes, err := x509.CreateCertificate(rand.Reader, ca, ca, &caPrivKey.PublicKey, caPrivKey)
	if err != nil {
		t.Fatal(err)
	}

	// pem encode
	caPEM := &bytes.Buffer{}
	if err := pem.Encode(caPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	}); err != nil {
		t.Fatal(err)
	}

	caPrivKeyPEM := new(bytes.Buffer)
	if err := pem.Encode(caPrivKeyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(caPrivKey),
	}); err != nil {
		t.Fatal(err)
	}

	cert := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName:   "test",
			Organization: []string{"Σ Acme Co"},
		},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		SubjectKeyId: []byte{1, 2, 3, 4, 6},
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageClientAuth,
			x509.ExtKeyUsageServerAuth,
		},
		KeyUsage: x509.KeyUsageDigitalSignature,
	}

	certPrivKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		t.Fatal(err)
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, cert, ca, &certPrivKey.PublicKey, caPrivKey)
	if err != nil {
		t.Fatal(err)
	}

	certPEM := new(bytes.Buffer)
	if err := pem.Encode(certPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	}); err != nil {
		t.Fatal(err)
	}

	certPrivKeyPEM := new(bytes.Buffer)
	if err := pem.Encode(certPrivKeyPEM, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(certPrivKey),
	}); err != nil {
		t.Fatal(err)
	}

	serverCert, err := tls.X509KeyPair(certPEM.Bytes(), certPrivKeyPEM.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	serverTLSConf := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
	}

	return serverTLSConf, caPEM.String()
}

type mockLogger struct {
	output []string
	r      logr.RuntimeInfo
}

func (m *mockLogger) Init(info logr.RuntimeInfo) { m.r = info }

func (m *mockLogger) Enabled(level int) bool {
	return true
}

func (m *mockLogger) Info(level int, msg string, keysAndValues ...interface{}) {
	m.output = append(m.output, "info: "+fmt.Sprintf(msg, keysAndValues...))
}

func (m *mockLogger) Error(err error, msg string, keysAndValues ...interface{}) {
	m.output = append(m.output, "error: "+fmt.Sprintf(msg, keysAndValues...)+": "+err.Error())
}

func (m *mockLogger) WithValues(keysAndValues ...interface{}) logr.LogSink {
	return m
}

func (m *mockLogger) WithName(name string) logr.LogSink {
	return m
}

func (m *mockLogger) assertLogged(t *testing.T, msg string) {
	t.Helper()
	re, err := regexp.Compile(msg)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.ContainsFunc(m.output, func(s string) bool {
		return re.MatchString(s)
	}) {
		t.Errorf("did not capture log: %q", msg)
	}
}

// asserts that the provided conditions contain a condition with the provided
// type, with a message that matches the provided msg.
func assertConditionMessage(t *testing.T, conditions []genericcondition.GenericCondition, type_, msg string) {
	t.Helper()

	re, err := regexp.Compile(msg)
	if err != nil {
		t.Fatal(err)
	}

	if slices.ContainsFunc(conditions, func(cond genericcondition.GenericCondition) bool {
		return cond.Type == type_ && re.MatchString(cond.Message)
	}) {
		return
	}

	t.Errorf("failed to find condition %q and message %q in conditions %#v", type_, msg, conditions)
}
