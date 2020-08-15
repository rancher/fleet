package cmds

import (
	"os"

	"github.com/rancher/fleet/modules/cli/controllermanifest"
	command "github.com/rancher/wrangler-cli"
	"github.com/spf13/cobra"
)

func NewOperator() *cobra.Command {
	return command.Command(&Operator{}, cobra.Command{
		Short: "Generate deployment manifest to run the fleet operator",
	})
}

type Operator struct {
	SystemNamespace string `usage:"Namespace that will be use in controller and agent cluster" default:"fleet-system"`
	ManagerImage    string `usage:"Image to use for controller"`
	AgentImage      string `usage:"Image to use for all agents"`
	CRDsOnly        bool   `usage:"Output CustomResourceDefinitions only"`
}

func (a *Operator) Run(cmd *cobra.Command, args []string) error {
	opts := &controllermanifest.Options{
		Namespace:    a.SystemNamespace,
		ManagerImage: a.ManagerImage,
		AgentImage:   a.AgentImage,
		CRDsOnly:     a.CRDsOnly,
	}

	return controllermanifest.OperatorManifest(os.Stdout, opts)
}
