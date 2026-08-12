package imagescan

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

func TestOptionsFromSecret(t *testing.T) {
	t.Run("uses docker config without CA", func(t *testing.T) {
		secret := &corev1.Secret{
			Type: corev1.SecretTypeDockerConfigJson,
			Data: map[string][]byte{
				".dockerconfigjson": []byte(`{"auths":{"registry.example.com":{"auth":"dGVzdDp0ZXN0"}}}`),
			},
		}

		options, err := optionsFromSecret(secret, "registry.example.com")
		if err != nil {
			t.Fatalf("optionsFromSecret() unexpected error: %v", err)
		}
		if len(options) != 1 {
			t.Fatalf("optionsFromSecret() = %d options, want 1", len(options))
		}
	})

	t.Run("uses custom CA when present", func(t *testing.T) {
		secret := &corev1.Secret{
			Type: corev1.SecretTypeDockerConfigJson,
			Data: map[string][]byte{
				".dockerconfigjson": []byte(`{"auths":{"registry.example.com":{"auth":"dGVzdDp0ZXN0"}}}`),
				"ca.crt":            testPEM(t),
			},
		}

		options, err := optionsFromSecret(secret, "registry.example.com")
		if err != nil {
			t.Fatalf("optionsFromSecret() unexpected error: %v", err)
		}
		if len(options) != 2 {
			t.Fatalf("optionsFromSecret() = %d options, want 2", len(options))
		}
	})

	t.Run("invalid ca cert is rejected", func(t *testing.T) {
		secret := &corev1.Secret{
			Type: corev1.SecretTypeDockerConfigJson,
			Data: map[string][]byte{
				".dockerconfigjson": []byte(`{"auths":{"registry.example.com":{"auth":"dGVzdDp0ZXN0"}}}`),
				"ca.crt":            []byte("not a cert"),
			},
		}

		if _, err := optionsFromSecret(secret, "registry.example.com"); err == nil {
			t.Fatal("optionsFromSecret() expected error, got nil")
		}
	})
}

func testPEM(t *testing.T) []byte {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() unexpected error: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "registry.example.com"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("CreateCertificate() unexpected error: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestLatestTag(t *testing.T) {
	var alphabeticalVersions = []string{"a", "b", "c"}

	tests := []struct {
		name, want string
		policy     fleet.ImagePolicyChoice
	}{
		{
			name: "alphabetical asc lowercase",
			policy: fleet.ImagePolicyChoice{
				Alphabetical: &fleet.AlphabeticalPolicy{Order: "asc"},
			},
			want: "a",
		},
		{
			name: "alphabetical asc uppercase",
			policy: fleet.ImagePolicyChoice{
				Alphabetical: &fleet.AlphabeticalPolicy{Order: "ASC"},
			},
			want: "a",
		},
		{
			name: "alphabetical desc lowercase",
			policy: fleet.ImagePolicyChoice{
				Alphabetical: &fleet.AlphabeticalPolicy{Order: "desc"},
			},
			want: "c",
		},
		{
			name: "alphabetical desc uppercase",
			policy: fleet.ImagePolicyChoice{
				Alphabetical: &fleet.AlphabeticalPolicy{Order: "DESC"},
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
