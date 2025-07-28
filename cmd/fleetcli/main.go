// Package main is the entry point for the fleet apply binary.
package main

import (
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
	"github.com/sirupsen/logrus"

	cmds "github.com/rancher/fleet/internal/cmd/cli"
	fleetapply "github.com/rancher/fleet/internal/cmd/cli/apply"
)

func main() {
	ctx := signals.SetupSignalContext()
	cmd := cmds.App()
	if err := cmd.ExecuteContext(ctx); err != nil {
		if strings.ToLower(os.Getenv(fleetapply.JSONOutputEnvVar)) == "true" {
			log := logrus.New()
			log.SetFormatter(&logrus.JSONFormatter{})
			// use a fleet specific field name so we are sure logs from other libraries
			// are not considered.
			log.WithFields(logrus.Fields{
				"fleetErrorMessage": err.Error(),
			}).Fatal("Fleet cli failed")
		} else {
			logrus.Fatal(err)
		}
	}
}
