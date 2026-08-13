// Package main is the entry point for the fleet apply binary.
package main

import (
	"log"
	"log/slog"
	"os"
	"strings"

	// Ensure GVKs are registered
	_ "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	_ "github.com/rancher/wrangler/v3/pkg/generated/controllers/apiextensions.k8s.io"
	_ "github.com/rancher/wrangler/v3/pkg/generated/controllers/apps"
	_ "github.com/rancher/wrangler/v3/pkg/generated/controllers/core"
	_ "github.com/rancher/wrangler/v3/pkg/generated/controllers/rbac"

	// Add non-default auth providers
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/rancher/wrangler/v3/pkg/signals"

	cmds "github.com/rancher/fleet/internal/cmd/cli"
	fleetapply "github.com/rancher/fleet/internal/cmd/cli/apply"
)

func main() {
	ctx := signals.SetupSignalContext()
	cmd := cmds.App()
	if err := cmd.ExecuteContext(ctx); err != nil {
		if strings.ToLower(os.Getenv(fleetapply.JSONOutputEnvVar)) == "true" {
			logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
			logger.Error("Fleet cli failed", "fleetErrorMessage", err.Error())
			os.Exit(1)
		} else {
			log.Fatal(err)
		}
	}
}
