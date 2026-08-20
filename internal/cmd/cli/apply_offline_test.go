package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	// Registers the fleet GVKs with the wrangler scheme used to render
	// bundles, the same way cmd/fleetcli does.
	_ "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
)

// setupOfflineApply prepares a working directory holding a single resource in
// ./some, and makes any attempt to reach a cluster fail by pointing the
// kubeconfig at a valid but empty one.
func setupOfflineApply(t *testing.T) {
	t.Helper()

	t.Chdir(t.TempDir())

	if err := os.MkdirAll("some", 0o700); err != nil {
		t.Fatalf("failed to create bundle dir: %v", err)
	}
	configMap := `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
data:
  key: value
`
	if err := os.WriteFile(filepath.Join("some", "configmap.yaml"), []byte(configMap), 0o600); err != nil {
		t.Fatalf("failed to write configmap: %v", err)
	}

	emptyKubeconfig := `apiVersion: v1
kind: Config
clusters: []
contexts: []
users: []
preferences: {}
`
	path := filepath.Join(t.TempDir(), "kubeconfig-empty.yaml")
	if err := os.WriteFile(path, []byte(emptyKubeconfig), 0o600); err != nil {
		t.Fatalf("failed to write kubeconfig: %v", err)
	}

	t.Setenv("KUBECONFIG", path)
	setKubeconfigFlag(t, path)
}

func newApplyForTest() *Apply {
	a := &Apply{}
	a.Namespace = "fleet-local"
	a.BundleCreationMaxConcurrency = 4
	// Set so that the git repository containing the test is not scanned.
	a.Commit = "0000000000000000000000000000000000000000"

	return a
}

func testCommand() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	return cmd
}

// TestApplyOutputWithoutCluster is a regression test for #5426: with --output,
// bundles are rendered locally, so a reachable cluster must not be required.
func TestApplyOutputWithoutCluster(t *testing.T) {
	for name, output := range map[string]string{
		"file":   "bundle.yaml",
		"stdout": "-",
	} {
		t.Run(name, func(t *testing.T) {
			setupOfflineApply(t)

			a := newApplyForTest()
			a.Output = output

			if err := a.Run(testCommand(), []string{"repo", "some"}); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if output == "-" {
				// Nothing to assert on beyond the command succeeding: the
				// bundle went to the process' stdout.
				return
			}

			b, err := os.ReadFile(output)
			if err != nil {
				t.Fatalf("expected %s to be written: %v", output, err)
			}
			if !strings.Contains(string(b), "kind: Bundle") {
				t.Errorf("expected a Bundle in %s, got %q", output, string(b))
			}
		})
	}
}

// TestApplyDrivenScanOutputWithoutCluster covers the same regression for
// --driven-scan, which goes through CreateBundlesDriven.
func TestApplyDrivenScanOutputWithoutCluster(t *testing.T) {
	setupOfflineApply(t)

	a := newApplyForTest()
	a.Output = "bundle.yaml"
	a.DrivenScan = true
	a.DrivenScanSeparator = ":"

	if err := a.Run(testCommand(), []string{"repo", "some"}); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	b, err := os.ReadFile("bundle.yaml")
	if err != nil {
		t.Fatalf("expected bundle.yaml to be written: %v", err)
	}
	if !strings.Contains(string(b), "kind: Bundle") {
		t.Errorf("expected a Bundle in bundle.yaml, got %q", string(b))
	}
}

// TestApplyWithoutOutputRequiresKubeconfig makes sure the offline path is not
// taken when bundles are meant to be created on a cluster, and that the
// failure is reported instead of exiting silently.
func TestApplyWithoutOutputRequiresKubeconfig(t *testing.T) {
	setupOfflineApply(t)

	a := newApplyForTest()

	err := a.Run(testCommand(), []string{"repo", "some"})
	if err == nil {
		t.Fatal("expected an error, got none")
	}
	if !strings.Contains(err.Error(), "kubeconfig") {
		t.Errorf("expected the error to mention the kubeconfig, got %v", err)
	}
}
