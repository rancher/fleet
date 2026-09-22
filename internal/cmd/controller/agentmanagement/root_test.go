package agentmanagement

import (
	"testing"

	"github.com/rancher/fleet/internal/cmd/logtest"
)

// TestRunConfiguresControllerRuntimeLogger guards against a regression where
// this entrypoint never calls ctrl.SetLogger: log.Log calls throughout the
// agentmanagement controllers (e.g. cluster/controller.go) are then silently
// dropped instead of printed.
func TestRunConfiguresControllerRuntimeLogger(t *testing.T) {
	logtest.AssertPackageConfiguresLogger(t)
}
