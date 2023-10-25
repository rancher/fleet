package cleanup

import (
	"fmt"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/pkg/version"
	"github.com/spf13/cobra"
)

type CleanUp struct {
	Kubeconfig string `usage:"kubeconfig file"`
	Namespace  string `usage:"namespace to watch" env:"NAMESPACE"`
}

func (c *CleanUp) Run(cmd *cobra.Command, args []string) error {
	if c.Namespace == "" {
		return fmt.Errorf("--namespace or env NAMESPACE is required to be set")
	}
	if err := start(cmd.Context(), c.Kubeconfig, c.Namespace); err != nil {
		return err
	}
	<-cmd.Context().Done()

	return nil
}

func App() *cobra.Command {
	return command.Command(&CleanUp{}, cobra.Command{
		Version: version.FriendlyVersion(),
		Use:     "cleanup",
	})
}
