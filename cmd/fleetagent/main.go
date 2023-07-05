// Package main is the entrypoint for the fleet-agent binary.
package main

import (
	_ "net/http/pprof"

	"github.com/rancher/fleet/internal/cmd/agent"

	command "github.com/rancher/wrangler-cli"
)

func main() {
	command.Main(agent.App())
}
