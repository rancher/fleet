package ssh_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"testing"

	golangssh "golang.org/x/crypto/ssh"

	"github.com/rancher/fleet/internal/ssh"
)

func generateECPrivateKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshalling key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

func generateKnownHostsEntry(t *testing.T, hostname string) (golangssh.PublicKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating host key: %v", err)
	}
	sshPubKey, err := golangssh.NewPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("creating SSH public key: %v", err)
	}
	entry := fmt.Sprintf("%s %s", hostname, string(golangssh.MarshalAuthorizedKey(sshPubKey)))
	return sshPubKey, entry
}

// fakeAddr implements net.Addr for use in HostKeyCallback tests.
type fakeAddr struct{ s string }

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return a.s }

func TestNewSSHPublicKeys_NoKnownHosts(t *testing.T) {
	keyPEM := generateECPrivateKeyPEM(t)
	pubKeys, err := ssh.NewSSHPublicKeys("git", keyPEM, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pubKeys == nil {
		t.Fatal("expected non-nil PublicKeys")
	}
	// With no known hosts, InsecureIgnoreHostKey must accept any host.
	if err := pubKeys.HostKeyCallback("any-host:22", fakeAddr{"any-host:22"}, nil); err != nil {
		t.Fatalf("InsecureIgnoreHostKey should accept any host, got: %v", err)
	}
}

func TestNewSSHPublicKeys_WithKnownHosts(t *testing.T) {
	keyPEM := generateECPrivateKeyPEM(t)
	hostPubKey, knownHostsEntry := generateKnownHostsEntry(t, "myhost.example.com")

	pubKeys, err := ssh.NewSSHPublicKeys("git", keyPEM, []byte(knownHostsEntry))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Matching key for the known host must be accepted.
	addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 22}
	if err := pubKeys.HostKeyCallback("myhost.example.com:22", addr, hostPubKey); err != nil {
		t.Fatalf("expected callback to accept matching key, got: %v", err)
	}

	// An unknown host must be rejected.
	if err := pubKeys.HostKeyCallback("unknown-host:22", addr, hostPubKey); err == nil {
		t.Fatal("expected error for unknown host, got nil")
	}
}

func TestNewSSHPublicKeys_InvalidKnownHosts(t *testing.T) {
	keyPEM := generateECPrivateKeyPEM(t)
	// Malformed known_hosts must be rejected at construction time.
	_, err := ssh.NewSSHPublicKeys("git", keyPEM, []byte("not-valid-known-hosts @@@@"))
	if err == nil {
		t.Fatal("expected error for invalid known_hosts, got nil")
	}
}
