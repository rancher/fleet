package apply

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rancher/fleet/internal/ocistorage"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/schemes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type failFirstBundleClient struct {
	client.Client
}

func (c *failFirstBundleClient) Create(ctx context.Context, object client.Object, options ...client.CreateOption) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if bundle, ok := object.(*fleet.Bundle); ok && bundle.Name == "repo-first" {
		return errors.New("failed to write first bundle")
	}
	return c.Client.Create(ctx, object, options...)
}

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

func TestCreateBundlesWritesSuccessfulBundlesAfterReadError(t *testing.T) {
	if err := schemes.Register(fleet.AddToScheme); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	t.Chdir(root)
	for _, name := range []string{"first", "last"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "configmap.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "fleet.yaml"), []byte("namespace: default\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	invalidDir := filepath.Join(root, "invalid")
	if err := os.Mkdir(invalidDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(invalidDir, "fleet.yaml"), []byte("helm: ["), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name          string
		createBundles func(context.Context, client.Client, record.EventRecorder, string, []string, Options) error
	}{
		{name: "recursive scan", createBundles: CreateBundles},
		{name: "driven scan", createBundles: CreateBundlesDriven},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := &bytes.Buffer{}
			err := test.createBundles(context.Background(), nil, nil, "repo", []string{"first", "invalid", "last"}, Options{
				Output:                       output,
				BundleCreationMaxConcurrency: 1,
				DrivenScanSeparator:          ":",
			})
			if err == nil {
				t.Fatal("expected invalid bundle to return an error")
			}
			if !strings.Contains(err.Error(), "failed to process bundle") {
				t.Fatalf("expected error for invalid bundle, got %v", err)
			}
			if got := output.String(); !strings.Contains(got, "name: repo-first") || !strings.Contains(got, "name: repo-last") {
				t.Fatalf("expected output to include valid bundles, got %q", got)
			}
		})
	}
}

func TestCreateBundlesReturnsReadErrorWhenAllBundlesFail(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	for _, name := range []string{"invalid-a", "invalid-b"} {
		if err := os.Mkdir(name, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(name, "fleet.yaml"), []byte("helm: ["), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	tests := map[string]func(context.Context, client.Client, record.EventRecorder, string, []string, Options) error{
		"recursive scan": CreateBundles,
		"driven scan":    CreateBundlesDriven,
	}
	for name, createBundles := range tests {
		t.Run(name, func(t *testing.T) {
			err := createBundles(context.Background(), nil, nil, "repo", []string{"invalid-a", "invalid-b"}, Options{Output: &bytes.Buffer{}, DrivenScanSeparator: ":"})
			if err == nil || strings.Count(err.Error(), "failed to process bundle") != 2 {
				t.Fatalf("expected bundle read error, got %v", err)
			}
		})
	}
}

func TestCreateBundlesWritesOtherBundlesAfterWriteError(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := fleet.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	t.Chdir(root)
	for _, name := range []string{"first", "last"} {
		if err := os.Mkdir(name, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(name, "configmap.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(name, "fleet.yaml"), []byte("namespace: default\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	tests := map[string]func(context.Context, client.Client, record.EventRecorder, string, []string, Options) error{
		"recursive scan": CreateBundles,
		"driven scan":    CreateBundlesDriven,
	}
	for name, createBundles := range tests {
		t.Run(name, func(t *testing.T) {
			kubeClient := &failFirstBundleClient{Client: fake.NewClientBuilder().WithScheme(scheme).Build()}
			err := createBundles(context.Background(), kubeClient, nil, "repo", []string{"first", "last"}, Options{
				Namespace:                    "fleet-local",
				BundleCreationMaxConcurrency: 1,
				DrivenScanSeparator:          ":",
			})
			if err == nil || !strings.Contains(err.Error(), "failed to write first bundle") {
				t.Fatalf("expected bundle write error, got %v", err)
			}
			lastBundle := &fleet.Bundle{}
			if err := kubeClient.Get(context.Background(), client.ObjectKey{Name: "repo-last", Namespace: "fleet-local"}, lastBundle); err != nil {
				t.Fatalf("expected last bundle to be written, got %v", err)
			}
		})
	}
}
