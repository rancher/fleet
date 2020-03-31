package cmds

import (
	"os"

	"github.com/rancher/fleet/modules/cli/managermanifest"
	"github.com/rancher/fleet/modules/cli/pkg/command"
	"github.com/spf13/cobra"
)

func NewManager() *cobra.Command {
	return command.Command(&Manager{}, cobra.Command{
		Short: "Generate deployment manifest to run the fleet manager",
	})
}

type Manager struct {
	SystemNamespace string `usage:"Namespace that will be use in manager and agent cluster" default:"fleet-system"`
	ManagerImage    string `usage:"Image to use for manager"`
	AgentImage      string `usage:"Image to use for all agents"`
	CRDsOnly        bool   `usage:"Output CustomResourceDefinitions only"`
}

func (a *Manager) Run(cmd *cobra.Command, args []string) error {
	opts := &managermanifest.Options{
		Namespace:    a.SystemNamespace,
		ManagerImage: a.ManagerImage,
		AgentImage:   a.AgentImage,
		CRDsOnly:     a.CRDsOnly,
	}

	return managermanifest.ManagerManifest(os.Stdout, opts)
}
