// Package main is the entry point for the fleet apply binary. (fleetapply)
package main

import (
	"github.com/rancher/fleet/internal/cli/cmds"

	command "github.com/rancher/wrangler-cli"

	// Ensure GVKs are registered
	_ "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	_ "github.com/rancher/wrangler/pkg/generated/controllers/apiextensions.k8s.io"
	_ "github.com/rancher/wrangler/pkg/generated/controllers/apps"
	_ "github.com/rancher/wrangler/pkg/generated/controllers/core"
	_ "github.com/rancher/wrangler/pkg/generated/controllers/rbac"

	// Add non-default auth providers
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

func main() {
	command.Main(cmds.App())
}
