package cleanup

import (
	"fmt"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/pkg/version"
	"github.com/spf13/cobra"
)

type CleanUp struct {
	Kubeconfig string `usage:"kubeconfig file"`
	Namespace  string `usage:"namespace to watch" env:"NAMESPACE"`
}

// HelpFunc hides the global flags from the help output
func (c *CleanUp) HelpFunc(cmd *cobra.Command, strings []string) {
	_ = cmd.Flags().MarkHidden("disable-gitops")
	_ = cmd.Flags().MarkHidden("disable-metrics")
	_ = cmd.Flags().MarkHidden("shard-id")
	cmd.Parent().HelpFunc()(cmd, strings)
}

func (c *CleanUp) Run(cmd *cobra.Command, args []string) error {
	if c.Namespace == "" {
		return fmt.Errorf("--namespace or env NAMESPACE is required to be set")
	}
	return start(cmd.Context(), c.Kubeconfig, c.Namespace)
}

func App() *cobra.Command {
	return command.Command(&CleanUp{}, cobra.Command{
		Version: version.FriendlyVersion(),
		Use:     "cleanup",
	})
}
