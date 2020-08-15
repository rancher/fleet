package cmds

import (
	"github.com/spf13/cobra"
)

func NewInstall() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Generate manifests for installing manager or agent",
	}
	cmd.AddCommand(
		NewManager(),
		NewAgent(),
		NewSimulator(),
	)
	return cmd
}
