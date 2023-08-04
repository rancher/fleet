package cli

import (
	"github.com/spf13/cobra"

	"github.com/rancher/fleet/internal/cmd/cli/cleanup"
	command "github.com/rancher/wrangler-cli"
)

func NewCleanUp() *cobra.Command {
	cmd := command.Command(&CleanUp{}, cobra.Command{
		Use:   "cleanup [flags]",
		Short: "Clean up outdated cluster registrations",
	})
	command.AddDebug(cmd, &Debug)
	return cmd
}

type CleanUp struct {
	Min int `usage:"Minimum delay between deletes in ms" name:"min"`
	Max int `usage:"Maximum delay between deletes in s" name:"max"`
}

func (a *CleanUp) Run(cmd *cobra.Command, args []string) error {
	opts := cleanup.Options{
		Min:    a.Min,
		Max:    a.Max,
		Factor: 1.1,
	}

	if a.Min == 0 {
		opts.Min = 10
	}

	if a.Max == 0 {
		opts.Max = 5
	}

	return cleanup.ClusterRegistrations(cmd.Context(), Client, opts)
}
