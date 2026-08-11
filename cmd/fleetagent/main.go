// Package main is the entrypoint for the fleet-agent binary.
package main

import (
	"log"
	_ "net/http/pprof"

	"github.com/rancher/fleet/internal/cmd/agent"

	"github.com/rancher/wrangler/v3/pkg/signals"
)

func main() {
	ctx := signals.SetupSignalContext()
	cmd := agent.App()
	if err := cmd.ExecuteContext(ctx); err != nil {
		log.Fatal(err)
	}
}
