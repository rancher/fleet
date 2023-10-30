package agent

import (
	"fmt"
	"log"
	"net/http"

	"github.com/spf13/cobra"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/pkg/version"
)

type UpstreamOptions struct {
	Kubeconfig string `usage:"kubeconfig file for agent's cluster"`
	Namespace  string `usage:"system namespace is the namespace, the agent runs in, e.g. cattle-fleet-system" env:"NAMESPACE"`
}

type FleetAgent struct {
	command.DebugConfig
	UpstreamOptions
	AgentScope string `usage:"An identifier used to scope the agent bundleID names, typically the same as namespace" env:"AGENT_SCOPE"`
}

func (a *FleetAgent) Run(cmd *cobra.Command, args []string) error {
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil)) // nolint:gosec // Debugging only
	}()

	if err := a.SetupDebug(); err != nil {
		return fmt.Errorf("failed to setup debug logging: %w", err)
	}

	if a.Namespace == "" {
		return fmt.Errorf("--namespace or env NAMESPACE is required to be set")
	}
	if err := start(cmd.Context(), a.Kubeconfig, a.Namespace, a.AgentScope); err != nil {
		return err
	}

	<-cmd.Context().Done()
	return nil
}

func App() *cobra.Command {
	root := command.Command(&FleetAgent{}, cobra.Command{
		Version: version.FriendlyVersion(),
	})
	root.AddCommand(NewClusterStatus())
	root.AddCommand(NewRegister())
	return root
}
