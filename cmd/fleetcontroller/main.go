// Package main provides the entrypoint for the fleet-controller binary.
package main

import (
	_ "net/http/pprof"

	"github.com/rancher/fleet/internal/cmd/controller"

	_ "github.com/rancher/wrangler/v2/pkg/generated/controllers/apiextensions.k8s.io"
	_ "github.com/rancher/wrangler/v2/pkg/generated/controllers/networking.k8s.io"
	"github.com/rancher/wrangler/v2/pkg/signals"
	"github.com/sirupsen/logrus"
)

func main() {
	ctx := signals.SetupSignalContext()
	cmd := controller.App()
	if err := cmd.ExecuteContext(ctx); err != nil {
		logrus.Fatal(err)
	}
}
