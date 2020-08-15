package cmds

import (
	"os"

	"github.com/rancher/fleet/modules/cli/apply"
	"github.com/rancher/fleet/modules/cli/pkg/writer"
	command "github.com/rancher/wrangler-cli"
	"github.com/spf13/cobra"
)

func NewApply() *cobra.Command {
	return command.Command(&Apply{}, cobra.Command{
		Short: "Render a bundle into a Kubernetes resource and apply it in the Fleet Manager",
	})
}

type Apply struct {
	BundleInputArgs
	OutputArgsNoDefault
	BundleNamePrefix string `usage:"All bundle names will be prefixed with this string"`
	File             string `usage:"Read full bundle contents from file" short:"f"`
	Compress         bool   `usage:"Force all resources to be compress" short:"c"`
	ServiceAccount   string `usage:"Service account to assign to bundle created" short:"a"`
}

func (a *Apply) Run(cmd *cobra.Command, args []string) error {
	opts := &apply.Options{
		BundleFile:     a.BundleFile,
		Output:         writer.NewDefaultNone(a.Output),
		Compress:       a.Compress,
		ServiceAccount: a.ServiceAccount,
	}

	if a.File == "-" {
		opts.BundleReader = os.Stdin
	} else if a.File != "" {
		f, err := os.Open(a.File)
		if err != nil {
			return err
		}
		defer f.Close()
		opts.BundleReader = f
	}
	return apply.Apply(cmd.Context(), Client, args, opts)
}
