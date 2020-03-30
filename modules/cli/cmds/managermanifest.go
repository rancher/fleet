package cmds

import (
	"github.com/rancher/fleet/modules/cli/managermanifest"
	"github.com/rancher/fleet/modules/cli/pkg/command"
	"github.com/rancher/fleet/modules/cli/pkg/writer"
	"github.com/spf13/cobra"
)

func NewManagerManifest() *cobra.Command {
	return command.Command(&ManagerManifest{}, cobra.Command{
		Short: "Generate deployment manifest to run the fleet manager",
	})
}

type ManagerManifest struct {
	OutputArgs

	Namespace    string `usage:"Namespace that will be use in manager and agent cluster" default:"fleet-system"`
	ManagerImage string `usage:"Image to use for manager"`
	AgentImage   string `usage:"Image to use for all agents"`
}

func (a *ManagerManifest) Run(cmd *cobra.Command, args []string) error {
	opts := &managermanifest.Options{
		Namespace:    a.Namespace,
		ManagerImage: a.ManagerImage,
		AgentImage:   a.AgentImage,
	}

	return managermanifest.ManagerManifest(writer.New(a.Output), opts)
}
