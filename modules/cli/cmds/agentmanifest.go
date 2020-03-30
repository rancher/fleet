package cmds

import (
	"io/ioutil"
	"time"

	"github.com/rancher/fleet/modules/cli/agentmanifest"
	"github.com/rancher/fleet/modules/cli/pkg/command"
	"github.com/rancher/fleet/modules/cli/pkg/writer"
	"github.com/spf13/cobra"
)

func NewAgentToken() *cobra.Command {
	return command.Command(&AgentToken{}, cobra.Command{
		Short: "Generate cluster group token and render manifest to register clusters into a specific cluster group",
	})
}

type AgentToken struct {
	OutputArgs

	SystemNamespace string `usage:"System namespace of the manager" default:"fleet-system"`
	TTL             string `usage:"How long the generated registration token is valid, 0 means forever" default:"1440m" short:"t"`
	CAFile          string `usage:"File containing optional CA cert for fleet management server" name:"ca-file" short:"c"`
	ServerURL       string `usage:"The full URL to the fleet management server"`
	Group           string `usage:"Cluster group to generate config for" default:"default" short:"g"`
}

func (a *AgentToken) Run(cmd *cobra.Command, args []string) error {
	opts := &agentmanifest.Options{
		Host: a.ServerURL,
	}

	if a.TTL != "" && a.TTL != "0" {
		ttl, err := time.ParseDuration(a.TTL)
		if err != nil {
			return err
		}
		opts.TTL = ttl
	}

	if a.CAFile != "" {
		ca, err := ioutil.ReadFile(a.CAFile)
		if err != nil {
			return err
		}
		opts.CA = ca
	}

	return agentmanifest.AgentManifest(cmd.Context(), a.SystemNamespace, a.Group, Client, writer.New(a.Output), opts)
}
