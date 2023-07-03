// Package main is the entrypoint for the fleet-agent binary. (fleetagent)
package main

import (
	_ "net/http/pprof"

	"github.com/rancher/fleet/internal/fleetagent/cmds"

	command "github.com/rancher/wrangler-cli"
)

func main() {
	command.Main(cmds.App())
}
