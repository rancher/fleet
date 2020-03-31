package main

import (
	"github.com/rancher/fleet/modules/cli/pkg/command"
	"github.com/rancher/fleet/pkg/fleetmanager"
	"github.com/rancher/fleet/pkg/version"
	"github.com/spf13/cobra"
)

type FleetManager struct {
	Kubeconfig string `usage:"Kubeconfig file"`
	Namespace  string `usage:"namespace to watch" default:"fleet-system" env:"NAMESPACE"`
}

func (f *FleetManager) Run(cmd *cobra.Command, args []string) error {
	if err := fleetmanager.Start(cmd.Context(), f.Namespace, f.Kubeconfig); err != nil {
		return err
	}

	<-cmd.Context().Done()
	return nil
}

func main() {
	cmd := command.Command(&FleetManager{}, cobra.Command{
		Version: version.FriendlyVersion(),
	})
	command.Main(cmd)
}
