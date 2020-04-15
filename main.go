package main

import (
	"github.com/rancher/fleet/modules/cli/cmds"
	"github.com/rancher/fleet/modules/cli/pkg/command"

	// Ensure GVKs are registered
	_ "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	_ "github.com/rancher/wrangler-api/pkg/generated/controllers/apiextensions.k8s.io"
	_ "github.com/rancher/wrangler-api/pkg/generated/controllers/apps"
	_ "github.com/rancher/wrangler-api/pkg/generated/controllers/core"
	_ "github.com/rancher/wrangler-api/pkg/generated/controllers/rbac"

	// Add non-default auth providers (excluding azure because of dependencies)
	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

func main() {
	command.Main(cmds.App())
}
