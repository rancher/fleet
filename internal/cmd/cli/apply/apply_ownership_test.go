package apply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const ownershipTestNamespace = "fleet-default"

func Test_checkBundleOwnership(t *testing.T) {
	bundle := func(repo string) *fleet.Bundle {
		b := &fleet.Bundle{ObjectMeta: metav1.ObjectMeta{Name: "shared"}}
		if repo != "" {
			b.Labels = map[string]string{fleet.RepoLabel: repo}
		}
		return b
	}

	tests := map[string]struct {
		existing, incoming string
		wantErr            string // substring naming the rejected updater; empty means no error
	}{
		"same owner":                     {existing: "app-prod", incoming: "app-prod"},
		"different owner":                {existing: "app-prod", incoming: "app-dev", wantErr: `cannot be updated by GitRepo "app-dev"`},
		"existing owned, incoming not":   {existing: "app-prod", incoming: "", wantErr: "cannot be updated by a fleet apply without a GitRepo"},
		"existing not owned":             {existing: "", incoming: "app-dev"},
		"neither owned (manual applies)": {existing: "", incoming: ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := checkBundleOwnership(bundle(tt.existing), bundle(tt.incoming))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an ownership error")
			}
			for _, want := range []string{`owned by GitRepo "app-prod"`, tt.wantErr} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("expected error to contain %q, got %v", want, err)
				}
			}
		})
	}
}

func TestCreateBundlesRejectsBundleOwnedByAnotherGitRepo(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeBundleDir(t, "prod", "name: shared\nhelm:\n  values:\n    env: prod\ntargets:\n- clusterName: prod\n")
	// No helm values: before the fix this deleted the owner's values secret.
	writeBundleDir(t, "dev", "name: shared\ntargets:\n- clusterName: dev\n")

	c := newOwnershipTestClient(t)

	if err := applyAs(c, "app-prod", "prod"); err != nil {
		t.Fatalf("applying app-prod: %v", err)
	}

	err := applyAs(c, "app-dev", "dev")
	if err == nil || !strings.Contains(err.Error(), `owned by GitRepo "app-prod"`) {
		t.Fatalf("expected ownership conflict error, got %v", err)
	}

	assertSharedBundle(t, c, "app-prod", "prod")

	// The original owner can still update its bundle.
	writeBundleDir(t, "prod", "name: shared\nhelm:\n  values:\n    env: prod\ntargets:\n- clusterName: prod-2\n")
	if err := applyAs(c, "app-prod", "prod"); err != nil {
		t.Fatalf("updating app-prod: %v", err)
	}
	assertSharedBundle(t, c, "app-prod", "prod-2")
}

func TestCreateBundlesWithoutExplicitNameDoNotCollide(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeBundleDir(t, "app", "targets:\n- clusterName: any\n")

	c := newOwnershipTestClient(t)
	for _, repo := range []string{"app-prod", "app-dev"} {
		if err := applyAs(c, repo, "app"); err != nil {
			t.Fatalf("applying %s: %v", repo, err)
		}
	}

	for _, repo := range []string{"app-prod", "app-dev"} {
		b := &fleet.Bundle{}
		key := client.ObjectKey{Name: repo + "-app", Namespace: ownershipTestNamespace}
		if err := c.Get(context.Background(), key, b); err != nil {
			t.Fatalf("expected bundle %s: %v", key.Name, err)
		}
		if got := b.Labels[fleet.RepoLabel]; got != repo {
			t.Fatalf("bundle %s: expected owner %q, got %q", key.Name, repo, got)
		}
	}
}

func TestCreateBundlesIgnoresRepoLabelFromFleetYAML(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeBundleDir(t, "prod", "name: shared\ntargets:\n- clusterName: prod\n")
	// app-dev's fleet.yaml claims to be app-prod to get past the ownership check.
	writeBundleDir(t, "dev", "name: shared\nlabels:\n  fleet.cattle.io/repo-name: app-prod\ntargets:\n- clusterName: dev\n")

	c := newOwnershipTestClient(t)
	if err := applyAs(c, "app-prod", "prod"); err != nil {
		t.Fatalf("applying app-prod: %v", err)
	}

	err := applyAs(c, "app-dev", "dev")
	if err == nil || !strings.Contains(err.Error(), `cannot be updated by GitRepo "app-dev"`) {
		t.Fatalf("expected ownership conflict error, got %v", err)
	}

	b := &fleet.Bundle{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "shared", Namespace: ownershipTestNamespace}, b); err != nil {
		t.Fatalf("getting bundle: %v", err)
	}
	if len(b.Spec.Targets) != 1 || b.Spec.Targets[0].ClusterName != "prod" {
		t.Fatalf("expected app-prod's target to be kept, got %+v", b.Spec.Targets)
	}
}

// TestCreateBundlesRejectedInRaceKeepsValuesSecret covers two GitRepos creating
// the same bundle at the same time: the loser doesn't see the bundle in the
// early check and is only rejected when saving, by which point it must not
// have deleted the winner's values secret.
func TestCreateBundlesRejectedInRaceKeepsValuesSecret(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeBundleDir(t, "prod", "name: shared\nhelm:\n  values:\n    env: prod\ntargets:\n- clusterName: prod\n")
	writeBundleDir(t, "dev", "name: shared\ntargets:\n- clusterName: dev\n")

	c := newOwnershipTestClient(t)
	if err := applyAs(c, "app-prod", "prod"); err != nil {
		t.Fatalf("applying app-prod: %v", err)
	}

	err := applyAs(&missFirstBundleGetClient{Client: c}, "app-dev", "dev")
	if err == nil || !strings.Contains(err.Error(), `owned by GitRepo "app-prod"`) {
		t.Fatalf("expected ownership conflict error, got %v", err)
	}

	assertSharedBundle(t, c, "app-prod", "prod")
}

// missFirstBundleGetClient reports the first Bundle it is asked for as not
// found, as if another apply created it right after that read.
type missFirstBundleGetClient struct {
	client.Client
	missed atomic.Bool
}

func (c *missFirstBundleGetClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*fleet.Bundle); ok && c.missed.CompareAndSwap(false, true) {
		return apierrors.NewNotFound(schema.GroupResource{Group: fleet.SchemeGroupVersion.Group, Resource: "bundles"}, key.Name)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func newOwnershipTestClient(t *testing.T) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := fleet.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).Build()
}

func writeBundleDir(t *testing.T, dir, fleetYAML string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	cm := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: " + dir + "\n"
	if err := os.WriteFile(filepath.Join(dir, "configmap.yaml"), []byte(cm), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fleet.yaml"), []byte(fleetYAML), 0o600); err != nil {
		t.Fatal(err)
	}
}

// applyAs mimics the gitjob, which labels bundles with the owning GitRepo's name.
func applyAs(c client.Client, repo, dir string) error {
	return CreateBundles(context.Background(), c, nil, repo, []string{dir}, Options{
		Namespace:                    ownershipTestNamespace,
		Labels:                       map[string]string{fleet.RepoLabel: repo},
		BundleCreationMaxConcurrency: 1,
	})
}

func assertSharedBundle(t *testing.T, c client.Client, wantRepo, wantCluster string) {
	t.Helper()
	key := client.ObjectKey{Name: "shared", Namespace: ownershipTestNamespace}

	b := &fleet.Bundle{}
	if err := c.Get(context.Background(), key, b); err != nil {
		t.Fatalf("getting bundle: %v", err)
	}
	if got := b.Labels[fleet.RepoLabel]; got != wantRepo {
		t.Fatalf("expected bundle owner %q, got %q", wantRepo, got)
	}
	if len(b.Spec.Targets) != 1 || b.Spec.Targets[0].ClusterName != wantCluster {
		t.Fatalf("expected single target for cluster %q, got %+v", wantCluster, b.Spec.Targets)
	}

	if err := c.Get(context.Background(), key, &corev1.Secret{}); err != nil {
		t.Fatalf("expected values secret to be intact: %v", err)
	}
}
