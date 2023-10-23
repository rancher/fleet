// Package cli sets up the CLI commands for the fleet apply binary.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/rancher/fleet/internal/client"
	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/pkg/version"
)

type Getter interface {
	Get() (*client.Client, error)
	GetNamespace() string
}

var (
	Client          Getter
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
		NewCleanUp(),
	)

	return root
}

type Fleet struct {
	SystemNamespace string `usage:"System namespace of the controller" default:"cattle-fleet-system"`
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
