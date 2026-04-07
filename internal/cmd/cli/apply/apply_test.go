package apply

import (
	"testing"

	"github.com/rancher/fleet/internal/ocistorage"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func Test_newOCISecret_usesInsecureSkipTLSKey(t *testing.T) {
	bundle := &fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bundle",
			Namespace: "fleet-local",
			UID:       types.UID("bundle-uid"),
		},
	}

	secret := newOCISecret("manifest-id", bundle, ocistorage.OCIOpts{
		Reference:       "registry.example.com/test",
		Username:        "user",
		Password:        "pass",
		AgentUsername:   "agent-user",
		AgentPassword:   "agent-pass",
		InsecureSkipTLS: true,
	})

	if got := string(secret.Data[ocistorage.OCISecretInsecureSkipTLS]); got != "true" {
		t.Fatalf("expected insecureSkipTLS=true, got %q", got)
	}

	if _, ok := secret.Data[ocistorage.OCISecretInsecure]; ok {
		t.Fatal("did not expect legacy insecure key in generated secret")
	}
}
