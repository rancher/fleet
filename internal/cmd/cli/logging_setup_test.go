package cli

import (
	"testing"

	"github.com/rancher/fleet/internal/cmd/logtest"
)

// TestCLIEntrypointsConfigureControllerRuntimeLogger guards against a
// regression where a CLI entrypoint stops configuring its controller-runtime
// logger.
func TestCLIEntrypointsConfigureControllerRuntimeLogger(t *testing.T) {
	logtest.AssertPackageConfiguresLogger(t)
}
