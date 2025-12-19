package cluster

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestHasAPIServerConfigChanged(t *testing.T) {
	// Helper to create hash from byte array (matching actual config usage)
	makeHash := func(data []byte) string {
		h, _ := hashStatusField(data)
		return h
	}

	tests := []struct {
		name          string
		cfg           *config.Config
		secret        *corev1.Secret
		cluster       *fleet.Cluster
		expectChanged bool
		expectError   bool
	}{
		{
			name: "no change when URL and CA match (from config)",
			cfg: &config.Config{
				APIServerURL:              "https://hello.world",
				APIServerCA:               []byte("foo"),
				AgentTLSMode:              "system-store",
				GarbageCollectionInterval: metav1.Duration{Duration: 10 * time.Minute},
			},
			secret: &corev1.Secret{
				Data: map[string][]byte{},
			},
			cluster: &fleet.Cluster{
				Status: fleet.ClusterStatus{
					APIServerURL:              "https://hello.world",
					APIServerCAHash:           makeHash([]byte("foo")),
					AgentTLSMode:              "system-store",
					GarbageCollectionInterval: &metav1.Duration{Duration: 10 * time.Minute},
				},
			},
			expectChanged: false,
		},
		{
			name: "change detected when URL changes (from config)",
			cfg: &config.Config{
				APIServerURL:              "https://hello.new.world",
				APIServerCA:               []byte("foo"),
				AgentTLSMode:              "system-store",
				GarbageCollectionInterval: metav1.Duration{Duration: 10 * time.Minute},
			},
			secret: &corev1.Secret{
				Data: map[string][]byte{},
			},
			cluster: &fleet.Cluster{
				Status: fleet.ClusterStatus{
					APIServerURL:              "https://hello.world",
					APIServerCAHash:           makeHash([]byte("foo")),
					AgentTLSMode:              "system-store",
					GarbageCollectionInterval: &metav1.Duration{Duration: 10 * time.Minute},
				},
			},
			expectChanged: true,
		},
		{
			name: "change detected when CA changes (from config)",
			cfg: &config.Config{
				APIServerURL:              "https://hello.world",
				APIServerCA:               []byte("new-foo"),
				AgentTLSMode:              "system-store",
				GarbageCollectionInterval: metav1.Duration{Duration: 10 * time.Minute},
			},
			secret: &corev1.Secret{
				Data: map[string][]byte{},
			},
			cluster: &fleet.Cluster{
				Status: fleet.ClusterStatus{
					APIServerURL:              "https://hello.world",
					APIServerCAHash:           makeHash([]byte("foo")),
					AgentTLSMode:              "system-store",
					GarbageCollectionInterval: &metav1.Duration{Duration: 10 * time.Minute},
				},
			},
			expectChanged: true,
		},
		{
			name: "no API config change when URL and CA match (from secret)",
			cfg: &config.Config{
				APIServerURL:              "https://hello.world",
				APIServerCA:               []byte("foo"),
				AgentTLSMode:              "system-store",
				GarbageCollectionInterval: metav1.Duration{Duration: 10 * time.Minute},
			},
			secret: &corev1.Secret{
				Data: map[string][]byte{
					"apiServerURL": []byte("https://hello.secret.world"),
					"apiServerCA":  []byte("secret-foo"),
				},
			},
			cluster: &fleet.Cluster{
				Status: fleet.ClusterStatus{
					APIServerURL:              "https://hello.secret.world",
					APIServerCAHash:           makeHash([]byte("secret-foo")),
					AgentTLSMode:              "system-store",
					GarbageCollectionInterval: &metav1.Duration{Duration: 10 * time.Minute},
				},
			},
			expectChanged: false,
		},
		{
			name: "no API config change when config URL/CA differs but secret values match",
			cfg: &config.Config{
				APIServerURL:              "https://hello.new.world",
				APIServerCA:               []byte("new-foo"),
				AgentTLSMode:              "system-store",
				GarbageCollectionInterval: metav1.Duration{Duration: 10 * time.Minute},
			},
			secret: &corev1.Secret{
				Data: map[string][]byte{
					"apiServerURL": []byte("https://hello.secret.world"),
					"apiServerCA":  []byte("secret-foo"),
				},
			},
			cluster: &fleet.Cluster{
				Status: fleet.ClusterStatus{
					APIServerURL:              "https://hello.secret.world",
					APIServerCAHash:           makeHash([]byte("secret-foo")),
					AgentTLSMode:              "system-store",
					GarbageCollectionInterval: &metav1.Duration{Duration: 10 * time.Minute},
				},
			},
			expectChanged: false,
		},
		{
			name: "error when config URL empty and not in secret",
			cfg: &config.Config{
				APIServerURL:              "",
				APIServerCA:               nil,
				AgentTLSMode:              "strict",
				GarbageCollectionInterval: metav1.Duration{Duration: 10 * time.Minute},
			},
			secret: &corev1.Secret{
				Data: map[string][]byte{},
			},
			cluster: &fleet.Cluster{
				Status: fleet.ClusterStatus{
					APIServerURL:              "",
					APIServerCAHash:           "",
					AgentTLSMode:              "system-store",
					GarbageCollectionInterval: &metav1.Duration{Duration: 10 * time.Minute},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			_ = fleet.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)

			r := &ClusterImportReconciler{
				Client:          fake.NewClientBuilder().WithScheme(scheme).Build(),
				Scheme:          scheme,
				SystemNamespace: "cattle-fleet-system",
			}

			changed, err := r.hasAPIServerConfigChanged(tt.cfg, tt.secret, tt.cluster)

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectChanged, changed, "config change detection mismatch")
			}
		})
	}
}
