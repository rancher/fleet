package cmds

import (
	"github.com/rancher/fleet/modules/cli/match"
	"github.com/rancher/fleet/modules/cli/pkg/command"
	"github.com/rancher/fleet/modules/cli/pkg/writer"
	"github.com/spf13/cobra"
)

func NewMatch() *cobra.Command {
	return command.Command(&Match{}, cobra.Command{
		Args:  cobra.ExactArgs(1),
		Short: "Match a bundle to a target and optional render the output",
	})
}

type Match struct {
	BundleInputArgs
	OutputArgsNoDefault
	Group      string            `usage:"Cluster group to match against" short:"g"`
	Label      map[string]string `usage:"Cluster labels to match against" short:"l"`
	GroupLabel map[string]string `usage:"Cluster group labels to match against" short:"L"`
	Target     string            `usage:"Explicit target to match" short:"t"`
}

func (m *Match) Run(cmd *cobra.Command, args []string) error {
	opts := &match.Options{
		Output:             writer.New(m.Output),
		BaseDir:            args[0],
		BundleFile:         m.BundleFile,
		ClusterGroup:       m.Group,
		ClusterLabels:      m.Label,
		ClusterGroupLabels: m.GroupLabel,
		Target:             m.Target,
	}
	return match.Match(cmd.Context(), opts)
}
