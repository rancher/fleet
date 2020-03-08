package cmds

import (
	"github.com/rancher/fleet/modules/cli/apply"
	"github.com/rancher/fleet/modules/cli/pkg/command"
	"github.com/rancher/fleet/modules/cli/pkg/writer"
	"github.com/spf13/cobra"
)

func NewApply() *cobra.Command {
	return command.Command(&Apply{}, cobra.Command{
		Short: "Render a bundle into a Kubernetes resource",
	})
}

type Apply struct {
	BundleInputArgs
	OutputArgsNoDefault
}

func (a *Apply) Run(cmd *cobra.Command, args []string) error {
	opts := &apply.Options{
		BundleFile: a.BundleFile,
		Output:     writer.NewDefaultNone(a.Output),
	}
	return apply.Apply(cmd.Context(), Client, args, opts)
}
