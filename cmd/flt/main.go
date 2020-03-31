package main

import (
	"github.com/rancher/fleet/modules/cli/cmds"
	"github.com/rancher/fleet/modules/cli/pkg/command"

	// Ensure GVKs are registered
	_ "github.com/rancher/wrangler-api/pkg/generated/controllers/apiextensions.k8s.io"

	// Add non-default auth providers (excluding azure because of dependencies)
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	_ "k8s.io/client-go/plugin/pkg/client/auth/openstack"
)

func main() {
	command.Main(cmds.App())
}
