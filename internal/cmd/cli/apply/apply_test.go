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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
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
			if !strings.Contains(err.Error(), "invalid:") {
				t.Fatalf("expected error to identify the invalid bundle path, got %v", err)
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
			if !strings.Contains(err.Error(), "invalid-a:") || !strings.Contains(err.Error(), "invalid-b:") {
				t.Fatalf("expected error to identify both invalid bundle paths, got %v", err)
			}
		})
	}
}

func TestCreateBundlesDrivenReturnsOneCancellationError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := CreateBundlesDriven(ctx, nil, nil, "repo", []string{"first", "last"}, Options{
		Output:              &bytes.Buffer{},
		DrivenScanSeparator: ":",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation error, got %v", err)
	}
	if strings.Count(err.Error(), context.Canceled.Error()) != 1 {
		t.Fatalf("expected one cancellation error, got %v", err)
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
			if !strings.Contains(err.Error(), "repo-first:") {
				t.Fatalf("expected error to identify the failing bundle name, got %v", err)
			}
			lastBundle := &fleet.Bundle{}
			if err := kubeClient.Get(context.Background(), client.ObjectKey{Name: "repo-last", Namespace: "fleet-local"}, lastBundle); err != nil {
				t.Fatalf("expected last bundle to be written, got %v", err)
			}
		})
	}
}

func TestCreateBundlesContinuesAfterWalkError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("cannot restrict directory permissions when running as root")
	}
	if err := schemes.Register(fleet.AddToScheme); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	t.Chdir(root)
	if err := os.MkdirAll(filepath.Join("scan", "ignored"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("scan", "ignored", "configmap.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("scan", "ignored", "fleet.yaml"), []byte("namespace: default\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("last", 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("last", "configmap.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: last\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("last", "fleet.yaml"), []byte("namespace: default\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	blockedDir := filepath.Join(root, "scan", "blocked")
	if err := os.Mkdir(blockedDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blockedDir, "fleet.yaml"), []byte("namespace: default\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blockedDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blockedDir, 0o750) })

	output := &bytes.Buffer{}
	err := CreateBundles(context.Background(), nil, nil, "repo", []string{"scan", "last"}, Options{
		Output:                       output,
		BundleCreationMaxConcurrency: 1,
	})
	if err == nil || !strings.Contains(err.Error(), "failed walking path") {
		t.Fatalf("expected a walk error for the blocked directory, got %v", err)
	}
	if got := output.String(); !strings.Contains(got, "name: repo-last") {
		t.Fatalf("expected output to include bundle from the path after the walk error, got %q with error %v", got, err)
	}
	if strings.Contains(output.String(), "name: repo-scan-ignored") {
		t.Fatalf("did not expect output from the directory walk that failed, got %q", output.String())
	}
}

func writeBundleDir(t *testing.T, dir, configMapName, fleetYAML string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "configmap.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+configMapName+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fleet.yaml"), []byte(fleetYAML), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCreateBundlesRejectsBundleOwnedByAnotherGitRepo(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := fleet.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	t.Chdir(root)
	writeBundleDir(t, "prod", "prod", "name: collision-demo\nhelm:\n  values:\n    replicas: 1\ntargets:\n- clusterName: cluster-a\n")
	writeBundleDir(t, "dev", "dev", "name: collision-demo\ntargets:\n- clusterName: cluster-b\n")
	writeBundleDir(t, "prod-update", "prod", "name: collision-demo\ntargets:\n- clusterName: cluster-c\n")
	writeBundleDir(t, "spoof", "dev", "name: collision-demo\nlabels:\n  "+fleet.RepoLabel+": app-prod\ntargets:\n- clusterName: cluster-b\n")

	tests := map[string]func(context.Context, client.Client, record.EventRecorder, string, []string, Options) error{
		"recursive scan": CreateBundles,
		"driven scan":    CreateBundlesDriven,
	}
	for name, createBundles := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			kubeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			apply := func(repo string, labels map[string]string, dir string) error {
				return createBundles(ctx, kubeClient, nil, repo, []string{dir}, Options{
					Namespace:           "fleet-default",
					Labels:              labels,
					DrivenScanSeparator: ":",
				})
			}
			key := client.ObjectKey{Name: "collision-demo", Namespace: "fleet-default"}

			if err := apply("app-prod", map[string]string{fleet.RepoLabel: "app-prod"}, "prod"); err != nil {
				t.Fatalf("expected first GitRepo to create the bundle, got %v", err)
			}
			original := &fleet.Bundle{}
			if err := kubeClient.Get(ctx, key, original); err != nil {
				t.Fatal(err)
			}

			err := apply("app-dev", map[string]string{fleet.RepoLabel: "app-dev"}, "dev")
			if err == nil {
				t.Fatal("expected bundle owned by another GitRepo to be rejected")
			}
			for _, s := range []string{`"collision-demo"`, `"app-prod"`, `"app-dev"`} {
				if !strings.Contains(err.Error(), s) {
					t.Fatalf("expected error to contain %s, got %v", s, err)
				}
			}

			otherKey := client.ObjectKey{Name: "app-dev-other", Namespace: "fleet-default"}
			if err := kubeClient.Create(ctx, &fleet.Bundle{ObjectMeta: metav1.ObjectMeta{
				Name:      otherKey.Name,
				Namespace: otherKey.Namespace,
				Labels:    map[string]string{fleet.RepoLabel: "app-dev"},
			}}); err != nil {
				t.Fatal(err)
			}
			err = apply("app-dev", map[string]string{fleet.RepoLabel: "app-dev"}, "spoof")
			if err == nil || !strings.Contains(err.Error(), fleet.RepoLabel) {
				t.Fatalf("expected fleet.yaml overriding the GitRepo label to be rejected, got %v", err)
			}
			if err := kubeClient.Get(ctx, otherKey, &fleet.Bundle{}); err != nil {
				t.Fatalf("expected rejected fleet.yaml label to prevent pruning bundles of the GitRepo, got %v", err)
			}

			err = apply("manual", nil, "dev")
			if err == nil || !strings.Contains(err.Error(), `"app-prod"`) {
				t.Fatalf("expected bundle without GitRepo label to be rejected, got %v", err)
			}

			current := &fleet.Bundle{}
			if err := kubeClient.Get(ctx, key, current); err != nil {
				t.Fatal(err)
			}
			if current.ResourceVersion != original.ResourceVersion {
				t.Fatalf("expected rejected collisions to leave the bundle unchanged, got %+v", current)
			}
			if err := kubeClient.Get(ctx, key, &corev1.Secret{}); err != nil {
				t.Fatalf("expected values secret of the existing bundle to be kept, got %v", err)
			}

			if err := apply("app-prod", map[string]string{fleet.RepoLabel: "app-prod"}, "prod-update"); err != nil {
				t.Fatalf("expected owning GitRepo to update the bundle, got %v", err)
			}
			if err := kubeClient.Get(ctx, key, current); err != nil {
				t.Fatal(err)
			}
			if got := current.Spec.Targets; len(got) != 1 || got[0].ClusterName != "cluster-c" {
				t.Fatalf("expected owning GitRepo update to change targets, got %+v", got)
			}
			if err := kubeClient.Get(ctx, key, &corev1.Secret{}); !apierrors.IsNotFound(err) {
				t.Fatalf("expected values secret to be deleted once the owning GitRepo removes the values, got %v", err)
			}
		})
	}
}

func TestCreateBundlesAdoptsBundleWithoutGitRepo(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := fleet.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	t.Chdir(root)
	writeBundleDir(t, "dev", "dev", "name: collision-demo\ntargets:\n- clusterName: cluster-b\n")

	ctx := context.Background()
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&fleet.Bundle{
		ObjectMeta: metav1.ObjectMeta{Name: "collision-demo", Namespace: "fleet-default"},
	}).Build()
	if err := CreateBundles(ctx, kubeClient, nil, "app-dev", []string{"dev"}, Options{
		Namespace: "fleet-default",
		Labels:    map[string]string{fleet.RepoLabel: "app-dev"},
	}); err != nil {
		t.Fatalf("expected bundle without GitRepo label to be adopted, got %v", err)
	}

	bundle := &fleet.Bundle{}
	if err := kubeClient.Get(ctx, client.ObjectKey{Name: "collision-demo", Namespace: "fleet-default"}, bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Labels[fleet.RepoLabel] != "app-dev" {
		t.Fatalf("expected adopted bundle to belong to GitRepo app-dev, got labels %v", bundle.Labels)
	}
	if got := bundle.Spec.Targets; len(got) != 1 || got[0].ClusterName != "cluster-b" {
		t.Fatalf("expected adopted bundle to use targets of GitRepo app-dev, got %+v", got)
	}
}

// TestCreateBundlesKeepsValuesSecretOnConcurrentCollision covers a bundle created by another GitRepo after the initial
// ownership check, which must not delete the values secret of that bundle.
func TestCreateBundlesKeepsValuesSecretOnConcurrentCollision(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := fleet.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	t.Chdir(root)
	writeBundleDir(t, "dev", "dev", "name: collision-demo\ntargets:\n- clusterName: cluster-b\n")

	ctx := context.Background()
	key := client.ObjectKey{Name: "collision-demo", Namespace: "fleet-default"}
	hideBundle := true
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&fleet.Bundle{ObjectMeta: metav1.ObjectMeta{
			Name:      key.Name,
			Namespace: key.Namespace,
			Labels:    map[string]string{fleet.RepoLabel: "app-prod"},
		}},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
				Labels:    map[string]string{fleet.RepoLabel: "app-prod"},
			},
			Data: map[string][]byte{"values.yaml": []byte(`{"replicas":1}`)},
		},
	).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, k client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*fleet.Bundle); ok && k == key && hideBundle {
				hideBundle = false
				return apierrors.NewNotFound(fleet.SchemeGroupVersion.WithResource("bundles").GroupResource(), k.Name)
			}
			return c.Get(ctx, k, obj, opts...)
		},
	}).Build()

	err := CreateBundles(ctx, kubeClient, nil, "app-dev", []string{"dev"}, Options{
		Namespace: "fleet-default",
		Labels:    map[string]string{fleet.RepoLabel: "app-dev"},
	})
	if err == nil || !strings.Contains(err.Error(), `"app-prod"`) {
		t.Fatalf("expected bundle owned by another GitRepo to be rejected, got %v", err)
	}
	if err := kubeClient.Get(ctx, key, &corev1.Secret{}); err != nil {
		t.Fatalf("expected values secret of the existing bundle to be kept, got %v", err)
	}
}

// TestCreateBundlesKeepsValuesSecretWrittenConcurrently covers a concurrent apply of the same GitRepo writing values
// after this apply saved the bundle without values, which must not delete the values secret of the newer write.
func TestCreateBundlesKeepsValuesSecretWrittenConcurrently(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := fleet.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	t.Chdir(root)
	writeBundleDir(t, "prod", "prod", "name: collision-demo\ntargets:\n- clusterName: cluster-a\n")

	ctx := context.Background()
	key := client.ObjectKey{Name: "collision-demo", Namespace: "fleet-default"}
	labels := map[string]string{fleet.RepoLabel: "app-prod"}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&fleet.Bundle{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, Labels: labels}},
	).WithInterceptorFuncs(interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if err := c.Update(ctx, obj, opts...); err != nil {
				return err
			}
			if _, ok := obj.(*fleet.Bundle); !ok {
				return nil
			}
			return c.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, Labels: labels},
				Data:       map[string][]byte{"values.yaml": []byte(`{"replicas":2}`)},
			})
		},
	}).Build()

	if err := CreateBundles(ctx, kubeClient, nil, "app-prod", []string{"prod"}, Options{
		Namespace: "fleet-default",
		Labels:    labels,
	}); err != nil {
		t.Fatalf("expected owning GitRepo to update the bundle, got %v", err)
	}
	if err := kubeClient.Get(ctx, key, &corev1.Secret{}); err != nil {
		t.Fatalf("expected values secret written concurrently to be kept, got %v", err)
	}
}

func TestCreateBundlesFailsOnBundleReadError(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := fleet.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	t.Chdir(root)
	writeBundleDir(t, "dev", "dev", "name: collision-demo\ntargets:\n- clusterName: cluster-b\n")

	ctx := context.Background()
	readErr := errors.New("bundle read failed")
	failRead := true
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, k client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*fleet.Bundle); ok && failRead {
				failRead = false
				return readErr
			}
			return c.Get(ctx, k, obj, opts...)
		},
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			t.Errorf("expected no object to be created, got %T %s", obj, obj.GetName())
			return errors.New("unexpected create")
		},
	}).Build()

	err := CreateBundles(ctx, kubeClient, nil, "app-dev", []string{"dev"}, Options{
		Namespace: "fleet-default",
		Labels:    map[string]string{fleet.RepoLabel: "app-dev"},
	})
	if !errors.Is(err, readErr) {
		t.Fatalf("expected bundle read error to be returned, got %v", err)
	}
}

func TestCreateBundlesGeneratesDistinctNamesPerGitRepo(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := fleet.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	t.Chdir(root)
	writeBundleDir(t, "app", "app", "targets:\n- clusterName: cluster-a\n")

	ctx := context.Background()
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	for _, repo := range []string{"app-prod", "app-dev"} {
		if err := CreateBundles(ctx, kubeClient, nil, repo, []string{"app"}, Options{
			Namespace: "fleet-default",
			Labels:    map[string]string{fleet.RepoLabel: repo},
		}); err != nil {
			t.Fatalf("expected GitRepo %s to create its bundle, got %v", repo, err)
		}
	}

	for _, repo := range []string{"app-prod", "app-dev"} {
		bundle := &fleet.Bundle{}
		if err := kubeClient.Get(ctx, client.ObjectKey{Name: repo + "-app", Namespace: "fleet-default"}, bundle); err != nil {
			t.Fatalf("expected bundle for GitRepo %s, got %v", repo, err)
		}
		if bundle.Labels[fleet.RepoLabel] != repo {
			t.Fatalf("expected bundle to belong to GitRepo %s, got labels %v", repo, bundle.Labels)
		}
	}
}
