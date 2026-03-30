package apply

import (
	"testing"

	"github.com/rancher/fleet/internal/ocistorage"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func Test_getKindNS(t *testing.T) {
	cases := []struct {
		name           string
		input          fleet.BundleResource
		expectedOutput fleet.OverwrittenResource
	}{
		{
			name: "templated contents",
			input: fleet.BundleResource{
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
			},
			expectedOutput: fleet.OverwrittenResource{
				Kind:      "ConfigMap",
				Name:      "Foo",
				Namespace: "my-namespace",
			},
		},
		{
			name: "templated name",
			input: fleet.BundleResource{
				Name: "folder/my-cm.yaml",
				Content: `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Values.name }}
  namespace: my-namespace
data:
  bar: baz
  name: Foo`,
				// No encoding
			},
			expectedOutput: fleet.OverwrittenResource{
				Kind:      "ConfigMap",
				Name:      "TEMPLATED",
				Namespace: "my-namespace",
			},
		},
		{
			name: "templated namespace",
			input: fleet.BundleResource{
				Name: "folder/my-cm.yaml",
				Content: `apiVersion: v1
kind: ConfigMap
metadata:
  name: Foo
  namespace: {{ .Values.namespace }}
data:
  bar: baz
  name: Foo`,
				// No encoding
			},
			expectedOutput: fleet.OverwrittenResource{
				Kind:      "ConfigMap",
				Name:      "Foo",
				Namespace: "TEMPLATED",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			or, err := getKindNS(tc.input, "my-bundle")
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if or != tc.expectedOutput {
				t.Fatalf("expected OverwrittenResource\n\t%v, got\n\t%v", tc.expectedOutput, or)
			}
		})
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
