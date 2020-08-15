package cmds

import (
	"github.com/rancher/fleet/modules/cli/pkg/writer"

	"github.com/rancher/fleet/modules/cli/controllermanifest"
	command "github.com/rancher/wrangler-cli"
	"github.com/spf13/cobra"
)

func NewManager() *cobra.Command {
	return command.Command(&Manager{}, cobra.Command{
		Short: "Generate deployment manifest to run the Fleet Manager",
	})
}

type Manager struct {
	SystemNamespace string `usage:"Namespace that will be use in controller" default:"fleet-system"`
	ManagerImage    string `usage:"Image to use for controller"`
	AgentImage      string `usage:"Image to use for all agents"`
	CRDsOnly        bool   `usage:"Output CustomResourceDefinitions only"`
	OutputArgs
}

func (a *Manager) Run(cmd *cobra.Command, args []string) error {
	opts := &controllermanifest.Options{
		Namespace:    a.SystemNamespace,
		ManagerImage: a.ManagerImage,
		AgentImage:   a.AgentImage,
		CRDsOnly:     a.CRDsOnly,
		Output:       a.Output,
	}

	output := writer.NewDefaultNone(a.Output)
	defer output.Close()

	return controllermanifest.OperatorManifest(output, opts)
}
