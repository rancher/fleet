package cmds

import (
	"github.com/spf13/cobra"
)

func NewInstall() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Generate manifests for installing server and agent",
	}
	cmd.AddCommand(
		NewManager(),
		NewAgentToken(),
		NewAgentConfig(),
	)
	return cmd
}
