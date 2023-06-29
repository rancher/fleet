// Package main provides the entrypoint for the fleet-controller binary (fleetcontroller)
package main

import (
	_ "net/http/pprof"

	controller "github.com/rancher/fleet/internal/fleetcontroller/cmds"

	command "github.com/rancher/wrangler-cli"
	_ "github.com/rancher/wrangler/pkg/generated/controllers/apiextensions.k8s.io"
	_ "github.com/rancher/wrangler/pkg/generated/controllers/networking.k8s.io"
)

func main() {
	command.Main(controller.App())
}
