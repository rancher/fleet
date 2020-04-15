package cmds

import (
	"io/ioutil"
	"os"
	"time"

	"github.com/rancher/fleet/modules/cli/pkg/command"
	"github.com/rancher/fleet/modules/cli/simulator"
	"github.com/rancher/fleet/pkg/config"
	"github.com/spf13/cobra"
)

func NewSimulator() *cobra.Command {
	return command.Command(&Simulator{}, cobra.Command{
		Short: "Generate manifest to install a cluster simulator",
	})
}

type Simulator struct {
	SystemNamespace string            `usage:"System namespace of the controller" default:"fleet-system"`
	TTL             string            `usage:"How long the generated registration token is valid, 0 means forever" default:"1440m" short:"t"`
	CAFile          string            `usage:"File containing optional CA cert for fleet controller cluster" name:"ca-file" short:"c"`
	NoCA            bool              `usage:"Use no custom CA for a fleet controller that is signed by a well known CA with a proper CN."`
	ServerURL       string            `usage:"The full URL to the fleet controller cluster"`
	Group           string            `usage:"Cluster group to generate config for" default:"default" short:"g"`
	Clusters        int               `usage:"Number of clusters to simulate" default:"100"`
	Image           string            `usage:"Simulator image"`
	Labels          map[string]string `usage:"Labels to apply to the new cluster on register" short:"l"`
}

func (s *Simulator) Run(cmd *cobra.Command, args []string) error {
	opts := &simulator.Options{
		Host: s.ServerURL,
		NoCA: s.NoCA,
	}

	if s.TTL != "" && s.TTL != "0" {
		ttl, err := time.ParseDuration(s.TTL)
		if err != nil {
			return err
		}
		opts.TTL = ttl
	}

	if s.CAFile != "" {
		ca, err := ioutil.ReadFile(s.CAFile)
		if err != nil {
			return err
		}
		opts.CA = ca
	}

	if s.Image == "" {
		s.Image = config.DefaultAgentSimulatorImage
	}

	return simulator.Simulator(cmd.Context(), s.Image, s.SystemNamespace, s.Group, s.Clusters, Client, os.Stdout, opts)
}
