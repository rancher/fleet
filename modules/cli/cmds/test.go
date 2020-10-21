package cmds

import (
	"os"

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
	Quiet      bool              `usage:"Just print the match and don't print the resources" short:"q"`
	Group      string            `usage:"Cluster group to match against" short:"g"`
	Label      map[string]string `usage:"Cluster labels to match against" short:"l"`
	GroupLabel map[string]string `usage:"Cluster group labels to match against" short:"L"`
	Target     string            `usage:"Explicit target to match" short:"t"`
}

func (m *Test) Run(cmd *cobra.Command, args []string) error {
	baseDir := "."
	if len(args) > 0 {
		baseDir = args[0]
	}

	opts := &match.Options{
		Output:             os.Stdout,
		BaseDir:            baseDir,
		BundleSpec:         m.File,
		BundleFile:         m.BundleFile,
		ClusterGroup:       m.Group,
		ClusterLabels:      m.Label,
		ClusterGroupLabels: m.GroupLabel,
		Target:             m.Target,
	}

	if m.Quiet {
		opts.Output = nil
	}

	if opts.ClusterGroup == "" &&
		len(opts.ClusterLabels) == 0 &&
		len(opts.ClusterGroupLabels) == 0 &&
		opts.Target == "" {
		opts.ClusterGroup = "default"
	}

	return match.Match(cmd.Context(), opts)
}
