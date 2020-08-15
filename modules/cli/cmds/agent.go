package cmds

import (
	"io/ioutil"
	"os"

	"github.com/rancher/fleet/modules/cli/agentmanifest"
	command "github.com/rancher/wrangler-cli"
	"github.com/spf13/cobra"
)

func NewAgent() *cobra.Command {
	return command.Command(&Agent{}, cobra.Command{
		Short: "Generate cluster group token and render manifest to register clusters into a specific cluster group",
		Args:  cobra.ExactArgs(1),
	})
}

type Agent struct {
	CAFile    string `usage:"File containing optional CA cert for fleet controller cluster" name:"ca-file" short:"c"`
	NoCA      bool   `usage:"Use no custom CA for a fleet controller that is signed by a well known CA with a proper CN."`
	ServerURL string `usage:"The full URL to the fleet controller cluster"`
}

func (a *Agent) Run(cmd *cobra.Command, args []string) error {
	opts := &agentmanifest.Options{
		Host: a.ServerURL,
		NoCA: a.NoCA,
	}

	if a.CAFile != "" {
		ca, err := ioutil.ReadFile(a.CAFile)
		if err != nil {
			return err
		}
		opts.CA = ca
	}

	return agentmanifest.AgentManifest(cmd.Context(), SystemNamespace, Client, os.Stdout, args[0], opts)
}
