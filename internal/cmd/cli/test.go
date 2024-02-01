package cli

import (
	"os"

	"github.com/spf13/cobra"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/cli/match"
)

func NewTest() *cobra.Command {
	return command.Command(&Test{}, cobra.Command{
		Args:       cobra.MaximumNArgs(1),
		Deprecated: "use target and deploy sub-commands instead.",
		Short:      "Match a bundle to a target and render the output (deprecated)",
	})
}

type Test struct {
	BundleInputArgs
	Quiet      bool              `usage:"Just print the match and don't print the resources" short:"q"`
	Group      string            `usage:"Cluster group to match against" short:"g"`
	Name       string            `usage:"Cluster name to match against" short:"N"`
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
		ClusterName:        m.Name,
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
