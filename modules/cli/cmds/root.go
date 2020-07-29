package cmds

import (
	"github.com/rancher/fleet/modules/cli/pkg/client"
	"github.com/rancher/fleet/modules/cli/pkg/command"
	"github.com/rancher/fleet/pkg/version"
	"github.com/spf13/cobra"
)

var (
	Client *client.Getter
	Debug  command.DebugConfig
)

func App() *cobra.Command {
	root := command.Command(&Fleet{}, cobra.Command{
		Version: version.FriendlyVersion(),
	})

	command.AddDebug(root, &Debug)
	root.AddCommand(
		NewApply(),
		NewTest(),
		NewInstall(),
	)

	return root
}

type Fleet struct {
	Namespace  string `usage:"namespace" env:"NAMESPACE" default:"default" short:"n" env:"NAMESPACE"`
	Kubeconfig string `usage:"kubeconfig for authentication" short:"k"`
}

func (r *Fleet) Run(cmd *cobra.Command, args []string) error {
	return cmd.Help()
}

func (r *Fleet) PersistentPre(cmd *cobra.Command, args []string) error {
	Debug.MustSetupDebug()
	Client = client.NewGetter(r.Kubeconfig, r.Namespace)
	return nil
}

type BundleInputArgs struct {
	BundleFile string `usage:"Location of the bundle.yaml" short:"b"`
}

type OutputArgs struct {
	Output string `usage:"Output contents to file or - for stdout"  short:"o" default:"-"`
}

type OutputArgsNoDefault struct {
	Output string `usage:"Output contents to file or - for stdout"  short:"o"`
}
