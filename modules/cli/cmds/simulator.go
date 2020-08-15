package cmds

import (
	"io/ioutil"
	"os"

	"github.com/rancher/fleet/modules/cli/simulator"
	"github.com/rancher/fleet/pkg/config"
	command "github.com/rancher/wrangler-cli"
	"github.com/spf13/cobra"
)

func NewSimulator() *cobra.Command {
	return command.Command(&Simulator{}, cobra.Command{
		Args:  cobra.ExactArgs(1),
		Short: "Generate manifest to install a cluster simulator",
	})
}

type Simulator struct {
	CAFile    string            `usage:"File containing optional CA cert for fleet controller cluster" name:"ca-file" short:"c"`
	NoCA      bool              `usage:"Use no custom CA for a fleet controller that is signed by a well known CA with a proper CN."`
	ServerURL string            `usage:"The full URL to the fleet controller cluster"`
	Clusters  int               `usage:"Number of clusters to simulate" default:"100"`
	Image     string            `usage:"Simulator image"`
	Labels    map[string]string `usage:"Labels to apply to the new cluster on register (example key=value,key2=value2)" short:"l"`
}

func (s *Simulator) Run(cmd *cobra.Command, args []string) error {
	opts := &simulator.Options{
		Host: s.ServerURL,
		NoCA: s.NoCA,
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

	return simulator.Simulator(cmd.Context(), s.Image, SystemNamespace, args[0], s.Clusters, Client, os.Stdout, opts)
}
