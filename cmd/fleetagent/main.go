package main

import (
	"fmt"

	"github.com/rancher/fleet/modules/agent/pkg/agent"
	"github.com/rancher/fleet/modules/agent/pkg/simulator"
	"github.com/rancher/fleet/pkg/version"
	command "github.com/rancher/wrangler-cli"
	"github.com/spf13/cobra"
)

type FleetAgent struct {
	Kubeconfig string `usage:"kubeconfig file"`
	Namespace  string `usage:"namespace to watch" env:"NAMESPACE"`
	Simulators int    `usage:"Numbers of simulators to run"`
}

func (a *FleetAgent) Run(cmd *cobra.Command, args []string) error {
	if a.Namespace == "" {
		return fmt.Errorf("--namespace or env NAMESPACE is required to be set")
	}
	if a.Simulators > 0 {
		return simulator.Simulate(cmd.Context(), a.Simulators, a.Kubeconfig, a.Namespace, "default")
	}
	if err := agent.Start(cmd.Context(), a.Kubeconfig, a.Namespace, nil); err != nil {
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
