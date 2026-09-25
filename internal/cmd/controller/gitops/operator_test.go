package gitops

import (
	"testing"

	"github.com/rancher/fleet/internal/cmd/logtest"
)

// TestRunConfiguresControllerRuntimeLogger guards against a regression where
// this entrypoint stops calling ctrl.SetLogger: log.Log calls throughout the
// gitops reconcilers are then silently dropped instead of printed.
func TestRunConfiguresControllerRuntimeLogger(t *testing.T) {
	logtest.AssertPackageConfiguresLogger(t)
}
