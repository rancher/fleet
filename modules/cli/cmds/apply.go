package cmds

import (
	"fmt"
	"os"

	"github.com/rancher/fleet/modules/cli/apply"
	"github.com/rancher/fleet/modules/cli/pkg/writer"
	command "github.com/rancher/wrangler-cli"
	"github.com/spf13/cobra"
)

func NewApply() *cobra.Command {
	return command.Command(&Apply{}, cobra.Command{
		Use:   "apply [flags] BUNDLE_NAME PATH...",
		Short: "Render a bundle into a Kubernetes resource and apply it in the Fleet Manager",
	})
}

type Apply struct {
	BundleInputArgs
	OutputArgsNoDefault
	Label          map[string]string `usage:"Labels to apply to created bundles" short:"l"`
	File           string            `usage:"Read full bundle contents from file" short:"f"`
	TargetsFile    string            `usage:"Addition source of targets and restrictions to be append"`
	Compress       bool              `usage:"Force all resources to be compress" short:"c"`
	ServiceAccount string            `usage:"Service account to assign to bundle created" short:"a"`
}

func (a *Apply) Run(cmd *cobra.Command, args []string) error {
	name := ""
	opts := &apply.Options{
		BundleFile:     a.BundleFile,
		Output:         writer.NewDefaultNone(a.Output),
		Compress:       a.Compress,
		ServiceAccount: a.ServiceAccount,
		Labels:         a.Label,
	}

	if a.File == "-" {
		opts.BundleReader = os.Stdin
		if len(args) != 1 {
			return fmt.Errorf("the bundle name is required as the first argument")
		}
		name = args[0]
	} else if a.File != "" {
		f, err := os.Open(a.File)
		if err != nil {
			return err
		}
		defer f.Close()
		opts.BundleReader = f
		if len(args) != 1 {
			return fmt.Errorf("the bundle name is required as the first argument")
		}
		name = args[0]
	} else if len(args) < 1 {
		return fmt.Errorf("at least one arguments is required BUNDLE_NAME")
	} else {
		name = args[0]
		args = args[1:]
	}

	return apply.Apply(cmd.Context(), Client, name, args, opts)
}
