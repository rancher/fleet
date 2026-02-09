package dump

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func Test_GitRepoFiltering(t *testing.T) {
	ctx := context.Background()
	logger := log.FromContext(ctx).WithName("test-fleet-dump")

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	// Create test data: 2 GitRepos with their bundles and bundledeployments
	objs := []runtime.Object{
		// GitRepo 1: my-repo
		&v1alpha1.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-repo",
				Namespace: "fleet-local",
			},
		},
		// GitRepo 2: other-repo
		&v1alpha1.GitRepo{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-repo",
				Namespace: "fleet-local",
			},
		},
		// Bundles from my-repo
		&v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-bundle-1",
				Namespace: "fleet-local",
				Labels: map[string]string{
					"fleet.cattle.io/repo-name": "my-repo",
				},
			},
		},
		&v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-bundle-2",
				Namespace: "fleet-local",
				Labels: map[string]string{
					"fleet.cattle.io/repo-name": "my-repo",
				},
			},
		},
		// Bundle from other-repo
		&v1alpha1.Bundle{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-bundle",
				Namespace: "fleet-local",
				Labels: map[string]string{
					"fleet.cattle.io/repo-name": "other-repo",
				},
			},
		},
		// BundleDeployments from my-repo bundles
		&v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-bd-1",
				Namespace: "cluster-ns-1",
				Labels: map[string]string{
					"fleet.cattle.io/bundle-name":      "my-bundle-1",
					"fleet.cattle.io/bundle-namespace": "fleet-local",
				},
			},
		},
		&v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-bd-2",
				Namespace: "cluster-ns-2",
				Labels: map[string]string{
					"fleet.cattle.io/bundle-name":      "my-bundle-2",
					"fleet.cattle.io/bundle-namespace": "fleet-local",
				},
			},
		},
		// BundleDeployment from other-repo
		&v1alpha1.BundleDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-bd",
				Namespace: "cluster-ns-1",
				Labels: map[string]string{
					"fleet.cattle.io/bundle-name":      "other-bundle",
					"fleet.cattle.io/bundle-namespace": "fleet-local",
				},
			},
		},
	}

	fakeDynClient := fake.NewSimpleDynamicClient(scheme, objs...)

	// Test filtering by GitRepo
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	opt := Options{
		FetchLimit: 0,
		Namespace:  "fleet-local",
		GitRepo:    "my-repo",
	}

	// Collect bundle names for my-repo
	bundleNames, err := collectBundleNamesByGitRepo(ctx, fakeDynClient, opt.Namespace, opt.GitRepo, opt.FetchLimit)
	if err != nil {
		t.Fatalf("failed to collect bundle names: %v", err)
	}

	if len(bundleNames) != 2 {
		t.Fatalf("expected 2 bundles for my-repo, got %d", len(bundleNames))
	}

	// Test addObjectsWithNameFilter for GitRepos
	err = addObjectsWithNameFilter(ctx, fakeDynClient, logger, "fleet.cattle.io", "v1alpha1", "gitrepos", tw, []string{"my-repo"}, opt)
	if err != nil {
		t.Fatalf("failed to add gitrepos: %v", err)
	}

	// Test addObjectsWithNameFilter for Bundles
	err = addObjectsWithNameFilter(ctx, fakeDynClient, logger, "fleet.cattle.io", "v1alpha1", "bundles", tw, bundleNames, opt)
	if err != nil {
		t.Fatalf("failed to add bundles: %v", err)
	}

	// Test addBundleDeployments with bundle name filter
	err = addBundleDeployments(ctx, fakeDynClient, logger, tw, bundleNames, opt)
	if err != nil {
		t.Fatalf("failed to add bundledeployments: %v", err)
	}

	tw.Close()
	gz.Close()

	// Read back the tar archive and verify contents
	gzReader, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()
	tarReader := tar.NewReader(gzReader)

	entries := make(map[string]bool)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to read tar header: %v", err)
		}

		// Read the content to verify it's valid YAML
		data, err := io.ReadAll(tarReader)
		if err != nil {
			t.Fatalf("failed to read tar content: %v", err)
		}

		// Verify it can be unmarshaled
		var obj map[string]interface{}
		if err := yaml.Unmarshal(data, &obj); err != nil {
			t.Fatalf("failed to unmarshal %s: %v", header.Name, err)
		}

		entries[header.Name] = true
	}

	// Verify expected entries are present
	expectedEntries := []string{
		"gitrepos_fleet-local_my-repo",
		"bundles_fleet-local_my-bundle-1",
		"bundles_fleet-local_my-bundle-2",
		"bundledeployments_cluster-ns-1_my-bd-1",
		"bundledeployments_cluster-ns-2_my-bd-2",
	}

	for _, expected := range expectedEntries {
		if !entries[expected] {
			t.Errorf("expected entry %q not found in archive", expected)
		}
	}

	// Verify excluded entries are not present
	excludedEntries := []string{
		"gitrepos_fleet-local_other-repo",
		"bundles_fleet-local_other-bundle",
		"bundledeployments_cluster-ns-1_other-bd",
	}

	for _, excluded := range excludedEntries {
		if entries[excluded] {
			t.Errorf("excluded entry %q should not be in archive", excluded)
		}
	}

	t.Logf("Archive contains %d entries (expected 5)", len(entries))
}

func Test_buildBundleNameSelector(t *testing.T) {
	tests := []struct {
		name        string
		namespace   string
		bundleNames []string
		expected    string
	}{
		{
			name:        "no bundle names",
			namespace:   "fleet-local",
			bundleNames: []string{},
			expected:    "fleet.cattle.io/bundle-namespace=fleet-local",
		},
		{
			name:        "single bundle name",
			namespace:   "fleet-local",
			bundleNames: []string{"bundle1"},
			expected:    "fleet.cattle.io/bundle-namespace=fleet-local,fleet.cattle.io/bundle-name in (bundle1)",
		},
		{
			name:        "multiple bundle names",
			namespace:   "fleet-local",
			bundleNames: []string{"bundle1", "bundle2", "bundle3"},
			expected:    "fleet.cattle.io/bundle-namespace=fleet-local,fleet.cattle.io/bundle-name in (bundle1,bundle2,bundle3)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildBundleNameSelector(tt.namespace, tt.bundleNames)
			if result != tt.expected {
				t.Errorf("buildBundleNameSelector() = %q, want %q", result, tt.expected)
			}
		})
	}
}
