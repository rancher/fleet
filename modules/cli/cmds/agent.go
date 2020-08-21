package cmds

import (
	"errors"
	"io/ioutil"
	"os"

	"github.com/rancher/fleet/modules/cli/agentmanifest"
	command "github.com/rancher/wrangler-cli"
	"github.com/spf13/cobra"
)

func NewAgent() *cobra.Command {
	return command.Command(&Agent{}, cobra.Command{
		Use:   "agent [flags] TOKEN_NAME",
		Short: "Generate cluster group token and render manifest to register clusters into a specific cluster group",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return errors.New("token name is required as the first and only argument")
			}
			return nil
		},
	})
}

type Agent struct {
	ClientID       string            `usage:"Unique id used to identify the cluster, must match Spec.ClientID"`
	AgentNamespace string            `usage:"Namespace to run the agent in" default:"fleet-agent-system"`
	CAFile         string            `usage:"File containing optional CA cert for fleet controller cluster" name:"ca-file" short:"c"`
	NoCA           bool              `usage:"Use no custom CA for a fleet controller that is signed by a well known CA with a proper CN."`
	ServerURL      string            `usage:"The full URL to the fleet controller cluster"`
	Labels         map[string]string `usage:"Labels to apply to cluster upon registration"`
}

func (a *Agent) Run(cmd *cobra.Command, args []string) error {
	opts := &agentmanifest.Options{
		Host:     a.ServerURL,
		NoCA:     a.NoCA,
		ClientID: a.ClientID,
	}

	if a.CAFile != "" {
		ca, err := ioutil.ReadFile(a.CAFile)
		if err != nil {
			return err
		}
		opts.CA = ca
	}

	return agentmanifest.AgentManifest(cmd.Context(), SystemNamespace, a.AgentNamespace, Client, os.Stdout, args[0], opts)
}
