package apply

import (
	"testing"

	"github.com/rancher/fleet/internal/ocistorage"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func Test_getKindNS(t *testing.T) {
	// TODO add test cases covering templating in name and namespace (less likely in kind)
	// → we should return an empty overwrite in such cases
	br := fleet.BundleResource{
		Name: "folder/my-cm.yaml",
		Content: `apiVersion: v1
kind: ConfigMap
metadata:
  name: Foo
  namespace: my-namespace
data:
  bar: baz
  name: {{ .Values.name }}`,
		// No encoding
	}

	or, err := getKindNS(br, "my-bundle")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if or.Kind != "ConfigMap" {
		t.Fatalf("expected ConfigMap kind, got %v", or.Kind)
	}

	if or.Name != "Foo" {
		t.Fatalf("expected name Foo, got %v", or.Name)
	}

	if or.Namespace != "my-namespace" {
		t.Fatalf("expected namespace my-namespace, got %v", or.Namespace)
	}

}

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
