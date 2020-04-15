package cmds

import (
	"os"

	"github.com/rancher/fleet/modules/cli/agentconfig"
	"github.com/rancher/fleet/modules/cli/pkg/command"
	"github.com/spf13/cobra"
)

func NewAgentConfig() *cobra.Command {
	return command.Command(&AgentConfig{}, cobra.Command{
		Short: "Generate cluster specific agent config",
	})
}

type AgentConfig struct {
	SystemNamespace string            `usage:"System namespace of the controller" default:"fleet-system"`
	Labels          map[string]string `usage:"Labels to apply to the new cluster on register" short:"l"`
}

func (a *AgentConfig) Run(cmd *cobra.Command, args []string) error {
	opts := &agentconfig.Options{
		Labels: a.Labels,
	}

	return agentconfig.AgentConfig(cmd.Context(), a.SystemNamespace, Client, os.Stdout, opts)
}
