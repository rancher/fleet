package main

import (
	"log"
	"net/http"
	_ "net/http/pprof"

	"github.com/rancher/fleet/pkg/agent"
	"github.com/rancher/fleet/pkg/fleetcontroller"
	"github.com/rancher/fleet/pkg/version"
	command "github.com/rancher/wrangler-cli"
	_ "github.com/rancher/wrangler/pkg/generated/controllers/apiextensions.k8s.io"
	_ "github.com/rancher/wrangler/pkg/generated/controllers/networking.k8s.io"
	"github.com/spf13/cobra"
)

var (
	debugConfig command.DebugConfig
)

type FleetManager struct {
	Kubeconfig    string `usage:"Kubeconfig file"`
	Namespace     string `usage:"namespace to watch" default:"fleet-system" env:"NAMESPACE"`
	DisableGitops bool   `usage:"disable gitops components" name:"disable-gitops"`
}

func (f *FleetManager) Run(cmd *cobra.Command, args []string) error {
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()
	debugConfig.MustSetupDebug()
	if err := fleetcontroller.Start(cmd.Context(), f.Namespace, f.Kubeconfig, f.DisableGitops); err != nil {
		return err
	}

	if debugConfig.Debug {
		agent.DebugLevel = debugConfig.DebugLevel
	}

	<-cmd.Context().Done()
	return nil
}

func main() {
	cmd := command.Command(&FleetManager{}, cobra.Command{
		Version: version.FriendlyVersion(),
	})
	cmd = command.AddDebug(cmd, &debugConfig)
	command.Main(cmd)
}
