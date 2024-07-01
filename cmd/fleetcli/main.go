// Package main is the entry point for the fleet apply binary.
package main

import (
	// Ensure GVKs are registered
	_ "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	_ "github.com/rancher/wrangler/v3/pkg/generated/controllers/apiextensions.k8s.io"
	_ "github.com/rancher/wrangler/v3/pkg/generated/controllers/apps"
	_ "github.com/rancher/wrangler/v3/pkg/generated/controllers/core"
	_ "github.com/rancher/wrangler/v3/pkg/generated/controllers/rbac"

	// Add non-default auth providers
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/rancher/wrangler/v3/pkg/signals"
	"github.com/sirupsen/logrus"

	cmds "github.com/rancher/fleet/internal/cmd/cli"
)

func main() {
	ctx := signals.SetupSignalContext()
	cmd := cmds.App()
	if err := cmd.ExecuteContext(ctx); err != nil {
		logrus.Fatal(err)
	}

}
