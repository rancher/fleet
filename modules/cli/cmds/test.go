package cmds

import (
	"os"

	"github.com/rancher/fleet/modules/cli/apply"
	"github.com/rancher/fleet/modules/cli/match"
	command "github.com/rancher/wrangler-cli"
	"github.com/spf13/cobra"
)

func NewTest() *cobra.Command {
	return command.Command(&Test{}, cobra.Command{
		Args:  cobra.MaximumNArgs(1),
		Short: "Match a bundle to a target and render the output",
	})
}

type Test struct {
	BundleInputArgs
	Quiet       bool              `usage:"Just print the match and don't print the resources" short:"q"`
	Group       string            `usage:"Cluster group to match against" short:"g"`
	Label       map[string]string `usage:"Cluster labels to match against" short:"l"`
	GroupLabel  map[string]string `usage:"Cluster group labels to match against" short:"L"`
	Target      string            `usage:"Explicit target to match" short:"t"`
	PrintBundle bool              `usage:"Don't run match and just output the generated bundle"`
}

func (m *Test) Run(cmd *cobra.Command, args []string) error {
	if m.PrintBundle {
		return apply.Apply(cmd.Context(), Client, args, &apply.Options{
			BundleFile: m.BundleFile,
			Output:     os.Stdout,
		})
	}

	baseDir := "."
	if len(args) > 0 {
		baseDir = args[0]
	}

	opts := &match.Options{
		Output:             os.Stdout,
		BaseDir:            baseDir,
		BundleFile:         m.BundleFile,
		ClusterGroup:       m.Group,
		ClusterLabels:      m.Label,
		ClusterGroupLabels: m.GroupLabel,
		Target:             m.Target,
	}

	if m.Quiet {
		opts.Output = nil
	}
	return match.Match(cmd.Context(), opts)
}
