package agentmanagement

import (
	"fmt"
	"os"
	"strconv"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/agent"
	"github.com/rancher/fleet/pkg/version"
	"github.com/spf13/cobra"
)

type AgentManagement struct {
	command.DebugConfig
	Kubeconfig       string `usage:"kubeconfig file"`
	Namespace        string `usage:"namespace to watch" env:"NAMESPACE"`
	DisableBootstrap bool   `usage:"disable local cluster components" name:"disable-bootstrap"`
}

// HelpFunc hides the global flag from the help output
func (a *AgentManagement) HelpFunc(cmd *cobra.Command, strings []string) {
	_ = cmd.Flags().MarkHidden("disable-metrics")
	_ = cmd.Flags().MarkHidden("shard-id")
	_ = cmd.Flags().MarkHidden("shard-node-selector")
	cmd.Parent().HelpFunc()(cmd, strings)
}

func (a *AgentManagement) PersistentPre(_ *cobra.Command, _ []string) error {
	// if debug is enabled in controller, enable in agents too (unless otherwise specified)
	propagateDebug, _ := strconv.ParseBool(os.Getenv("FLEET_PROPAGATE_DEBUG_SETTINGS_TO_AGENTS"))
	if propagateDebug && a.Debug {
		agent.DebugEnabled = true
		agent.DebugLevel = a.DebugLevel
	}

	return nil
}

func (a *AgentManagement) Run(cmd *cobra.Command, args []string) error {
	if a.Namespace == "" {
		return fmt.Errorf("--namespace or env NAMESPACE is required to be set")
	}
	return start(cmd.Context(), a.Kubeconfig, a.Namespace, a.DisableBootstrap)
}

func App() *cobra.Command {
	return command.Command(&AgentManagement{}, cobra.Command{
		Version: version.FriendlyVersion(),
		Use:     "agentmanagement",
	})
}
