// Package cli sets up the CLI commands for the fleet apply binary.
package cli

import (
	"fmt"

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
	Client Getter
)

func App() *cobra.Command {
	root := command.Command(&Fleet{}, cobra.Command{
		Version:       version.FriendlyVersion(),
		SilenceUsage:  true,
		SilenceErrors: true,
	})

	root.AddCommand(
		NewApply(),
		NewTest(),
		NewCleanUp(),
	)

	return root
}

type Fleet struct {
	command.DebugConfig
	SystemNamespace string `usage:"System namespace of the controller" default:"cattle-fleet-system"`
	Namespace       string `usage:"namespace" env:"NAMESPACE" default:"fleet-local" short:"n"`
	Kubeconfig      string `usage:"kubeconfig for authentication" short:"k"`
	Context         string `usage:"kubeconfig context for authentication"`
}

func (r *Fleet) Run(cmd *cobra.Command, _ []string) error {
	return cmd.Help()
}

func (r *Fleet) PersistentPre(_ *cobra.Command, _ []string) error {
	if err := r.SetupDebug(); err != nil {
		return fmt.Errorf("failed to setup debug logging: %w", err)
	}
	Client = client.NewGetter(r.Kubeconfig, r.Context, r.Namespace)
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
