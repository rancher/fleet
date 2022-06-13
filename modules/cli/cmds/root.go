package cmds

import (
	"github.com/rancher/fleet/modules/cli/pkg/client"
	"github.com/rancher/fleet/pkg/version"
	command "github.com/rancher/wrangler-cli"
	"github.com/spf13/cobra"
)

var (
	Client          *client.Getter
	SystemNamespace string
	Debug           command.DebugConfig
)

func App() *cobra.Command {
	root := command.Command(&Fleet{}, cobra.Command{
		Version:       version.FriendlyVersion(),
		SilenceUsage:  true,
		SilenceErrors: true,
	})

	command.AddDebug(root, &Debug)
	root.AddCommand(
		NewApply(),
		NewTest(),
	)

	return root
}

type Fleet struct {
	SystemNamespace string `usage:"System namespace of the controller" default:"fleet-system"`
	Namespace       string `usage:"namespace" env:"NAMESPACE" default:"fleet-local" short:"n"`
	Kubeconfig      string `usage:"kubeconfig for authentication" short:"k"`
	Context         string `usage:"kubeconfig context for authentication"`
}

func (r *Fleet) Run(cmd *cobra.Command, args []string) error {
	return cmd.Help()
}

func (r *Fleet) PersistentPre(cmd *cobra.Command, args []string) error {
	Debug.MustSetupDebug()
	Client = client.NewGetter(r.Kubeconfig, r.Context, r.Namespace)
	SystemNamespace = r.SystemNamespace
	return nil
}

type BundleInputArgs struct {
	File       string `usage:"Location of the fleet.yaml" short:"f"`
	BundleFile string `usage:"Location of the raw Bundle resource yaml" short:"b"`
}

type OutputArgs struct {
	Output string `usage:"Output contents to file or - for stdout"  short:"o" default:"-"`
}

type OutputArgsNoDefault struct {
	Output string `usage:"Output contents to file or - for stdout"  short:"o"`
}
