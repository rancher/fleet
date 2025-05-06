// Package cli sets up the CLI commands for the fleet apply binary.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/rancher/fleet/internal/client"
	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/cli/gitcloner"
	"github.com/rancher/fleet/pkg/version"
)

type Getter interface {
	Get() (*client.Client, error)
	GetNamespace() string
}

var (
	Client Getter
)

const JSONOutputEnvVar = "FLEET_JSON_OUTPUT"

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

		NewTarget(),
		NewDeploy(),
		gitcloner.NewCmd(gitcloner.New()),
	)

	return root
}

type Fleet struct {
}

func (r *Fleet) Run(cmd *cobra.Command, _ []string) error {
	return cmd.Help()
}

type FleetClient struct {
	command.DebugConfig
	Namespace  string `usage:"namespace" env:"NAMESPACE" default:"fleet-local" short:"n"`
	Kubeconfig string `usage:"kubeconfig for authentication" short:"k"`
	Context    string `usage:"kubeconfig context for authentication"`
}

type BundleInputArgs struct {
	File       string `usage:"Location of the fleet.yaml" short:"f"`
	BundleFile string `usage:"Location of the raw Bundle resource yaml" short:"b"`
}

type OutputArgsNoDefault struct {
	Output string `usage:"Output contents to file or - for stdout"  short:"o"`
}
