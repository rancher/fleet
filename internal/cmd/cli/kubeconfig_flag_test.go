package cli

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
)

// kubeconfigCommands returns every subcommand which registers --kubeconfig, so
// that the flag is covered wherever it is offered rather than on apply alone.
func kubeconfigCommands() map[string]func() *cobra.Command {
	return map[string]func() *cobra.Command{
		"apply":               NewApply,
		"bundlediff":          NewBundleDiff,
		"clusterregistration": NewClusterRegistration,
		"deploy":              NewDeploy,
		"gitjob":              NewGitjob,
		"monitor":             NewMonitor,
		"target":              NewTarget,
	}
}

// writeKubeconfig writes a minimal, valid kubeconfig pointing at a known
// server and returns its path.
func writeKubeconfig(t *testing.T, server string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig.yaml")
	content := `apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: ` + server + `
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
users:
- name: test
  user: {}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write kubeconfig: %v", err)
	}
	return path
}

// resetKubeconfigFlag clears the kubeconfig path used by ctrl.GetConfig. That
// path lives in a controller-runtime package global, shared by every test in
// this binary, so a test which sets it - by parsing --kubeconfig or otherwise
// - has to clear it again. Otherwise it leaks into whatever runs next, which
// under `go test -shuffle` is not the test the author had in mind.
func resetKubeconfigFlag(t *testing.T) {
	t.Helper()

	fs := flag.NewFlagSet("", flag.ContinueOnError)
	ctrl.RegisterFlags(fs) // re-binding resets the global to ""
	if err := fs.Parse(nil); err != nil {
		t.Errorf("failed to reset --kubeconfig: %v", err)
	}
}

// setKubeconfigFlag points the kubeconfig used by ctrl.GetConfig at path, and
// restores it afterwards. The flag takes precedence over the in-cluster
// config, so tests behave the same inside and outside a pod.
func setKubeconfigFlag(t *testing.T, path string) {
	t.Helper()

	fs := flag.NewFlagSet("", flag.ContinueOnError)
	ctrl.RegisterFlags(fs)
	if err := fs.Parse([]string{"--kubeconfig", path}); err != nil {
		t.Fatalf("failed to set --kubeconfig: %v", err)
	}

	t.Cleanup(func() { resetKubeconfigFlag(t) })
}

// TestKubeconfigFlagIsHonored is a regression test for #5170: the subcommands
// which talk to a cluster must honor --kubeconfig, i.e. the flag must be wired
// to the config loader used by ctrl.GetConfig (the one the commands actually
// use), not to a dead struct field.
func TestKubeconfigFlagIsHonored(t *testing.T) {
	const server = "https://example.test:6443"

	for name, newCmd := range kubeconfigCommands() {
		t.Run(name, func(t *testing.T) {
			kubeconfig := writeKubeconfig(t, server)

			cmd := newCmd()
			t.Cleanup(func() { resetKubeconfigFlag(t) })

			// Parsing --kubeconfig must be accepted (not an "unknown flag"
			// error). ParseFlags merges persistent+local flags as cobra does
			// at execution time.
			if err := cmd.ParseFlags([]string{"--kubeconfig", kubeconfig}); err != nil {
				t.Fatalf("failed to parse --kubeconfig: %v", err)
			}

			// ...and it must actually reach the config loader the command uses.
			cfg, err := ctrl.GetConfig()
			if err != nil {
				t.Fatalf("ctrl.GetConfig() failed: %v", err)
			}
			if cfg.Host != server {
				t.Errorf("--kubeconfig was not honored: got host %q, want %q", cfg.Host, server)
			}
		})
	}
}

// TestKubeconfigShorthand makes sure the -k shorthand for --kubeconfig keeps
// working. The pre-existing FleetClient.Kubeconfig field exposed -k; the fix
// must not silently drop it.
//
// This parses -k rather than reading Flag.Shorthand: pflag records shorthands
// in a lookup table while the flag is added, so a Shorthand set after the fact
// shows up in --help while -k is still rejected at parse time.
func TestKubeconfigShorthand(t *testing.T) {
	const server = "https://shorthand.test:6443"

	for name, newCmd := range kubeconfigCommands() {
		t.Run(name, func(t *testing.T) {
			kubeconfig := writeKubeconfig(t, server)

			cmd := newCmd()
			t.Cleanup(func() { resetKubeconfigFlag(t) })

			if err := cmd.ParseFlags([]string{"-k", kubeconfig}); err != nil {
				t.Fatalf("failed to parse -k: %v", err)
			}

			cfg, err := ctrl.GetConfig()
			if err != nil {
				t.Fatalf("ctrl.GetConfig() failed: %v", err)
			}
			if cfg.Host != server {
				t.Errorf("-k was not honored: got host %q, want %q", cfg.Host, server)
			}
		})
	}
}
