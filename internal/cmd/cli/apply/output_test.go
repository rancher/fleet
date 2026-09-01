package apply

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	// Registers the fleet GVKs with the wrangler scheme used by printToOutput,
	// the same way cmd/fleetcli does.
	_ "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
)

// TestCreateBundlesWithOutputNeverUsesClient is the invariant the CLI relies
// on when it skips building a client: with Options.Output set, neither the
// client nor the recorder is touched, so passing nil for both has to be safe.
func TestCreateBundlesWithOutputNeverUsesClient(t *testing.T) {
	// globDirs strips leading slashes, so base dirs have to be relative to the
	// working directory.
	t.Chdir(t.TempDir())
	writeConfigMap(t, "some")

	for name, opts := range map[string]Options{
		"scan":        {Namespace: "fleet-local"},
		"driven scan": {Namespace: "fleet-local", DrivenScan: true, DrivenScanSeparator: ":"},
	} {
		t.Run(name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			opts := opts
			opts.Output = buf

			create := CreateBundles
			if opts.DrivenScan {
				create = CreateBundlesDriven
			}

			err := create(context.Background(), nil, nil, "test-bundle", []string{"some"}, opts)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if !strings.Contains(buf.String(), "kind: Bundle") {
				t.Errorf("expected a Bundle in the output, got %q", buf.String())
			}
		})
	}
}

func writeConfigMap(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("failed to create %s: %v", dir, err)
	}
	content := `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
data:
  key: value
`
	if err := os.WriteFile(filepath.Join(dir, "configmap.yaml"), []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write configmap: %v", err)
	}
}
