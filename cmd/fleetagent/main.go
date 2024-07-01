// Package main is the entrypoint for the fleet-agent binary.
package main

import (
	_ "net/http/pprof"

	"github.com/rancher/fleet/internal/cmd/agent"

	"github.com/rancher/wrangler/v3/pkg/signals"
	"github.com/sirupsen/logrus"
)

func main() {
	ctx := signals.SetupSignalContext()
	cmd := agent.App()
	if err := cmd.ExecuteContext(ctx); err != nil {
		logrus.Fatal(err)
	}
}
