package cmds

import (
	"io/ioutil"
	"time"

	"github.com/rancher/fleet/modules/cli/agentmanifest"
	"github.com/rancher/fleet/modules/cli/pkg/command"
	"github.com/rancher/fleet/modules/cli/pkg/writer"
	"github.com/spf13/cobra"
)

func NewAgentManifest() *cobra.Command {
	return command.Command(&AgentManifest{}, cobra.Command{
		Short: "Generate agent deployment",
	})
}

type AgentManifest struct {
	OutputArgs

	TTL        string            `usage:"How long the generated registration token is valid, 0 means forever" default:"1440m" short:"t"`
	CAFile     string            `usage:"File containing optional CA cert for fleet management server" name:"ca-file" short:"c"`
	Host       string            `usage:"The full URL to the fleet management server"`
	Group      string            `usage:"Cluster group to generate config for" default:"default" short:"g"`
	Labels     map[string]string `usage:"Labels to apply to the new cluster on register" short:"l"`
	ConfigOnly bool              `usage:"Output only the agent config that can be appended to generic agent register"`
}

func (a *AgentManifest) Run(cmd *cobra.Command, args []string) error {
	opts := &agentmanifest.Options{
		Host:       a.Host,
		Labels:     a.Labels,
		ConfigOnly: a.ConfigOnly,
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

	return agentmanifest.AgentManifest(cmd.Context(), a.Group, Client, writer.New(a.Output), opts)
}
