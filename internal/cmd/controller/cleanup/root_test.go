package cleanup

import (
	"testing"

	"github.com/rancher/fleet/internal/cmd/logtest"
)

// TestRunConfiguresControllerRuntimeLogger guards against a regression where
// this entrypoint never calls ctrl.SetLogger: log.Log calls in the cleanup
// controller are then silently dropped instead of printed.
func TestRunConfiguresControllerRuntimeLogger(t *testing.T) {
	logtest.AssertPackageConfiguresLogger(t)
}
