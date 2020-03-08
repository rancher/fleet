package main

import (
	"github.com/rancher/fleet/modules/agent/pkg/agent"
	"github.com/rancher/fleet/modules/cli/pkg/command"
	"github.com/rancher/fleet/pkg/version"
	"github.com/spf13/cobra"
)

type FleetAgent struct {
	Kubeconfig string `usage:"kubeconfig file"`
	Namespace  string `usage:"namespace to watch" default:"fleet-system"`
}

func (a *FleetAgent) Run(cmd *cobra.Command, args []string) error {
	if err := agent.Start(cmd.Context(), a.Kubeconfig, a.Namespace); err != nil {
		return err
	}
	<-cmd.Context().Done()
	return cmd.Context().Err()
}

func main() {
	cmd := command.Command(&FleetAgent{}, cobra.Command{
		Version: version.FriendlyVersion(),
	})
	command.Main(cmd)
}
