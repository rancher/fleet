package cmds

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/rancher/fleet/internal/fleetagent/agent"
	"github.com/rancher/fleet/pkg/version"

	command "github.com/rancher/wrangler-cli"
)

var (
	debugConfig command.DebugConfig
)

type FleetAgent struct {
	Kubeconfig      string `usage:"kubeconfig file"`
	Namespace       string `usage:"namespace to watch" env:"NAMESPACE"`
	AgentScope      string `usage:"An identifier used to scope the agent bundleID names, typically the same as namespace" env:"AGENT_SCOPE"`
	CheckinInterval string `usage:"How often to post cluster status" env:"CHECKIN_INTERVAL"`
}

func (a *FleetAgent) Run(cmd *cobra.Command, args []string) error {
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil)) // nolint:gosec // Debugging only
	}()

	debugConfig.MustSetupDebug()
	var (
		opts agent.Options
		err  error
	)

	if a.CheckinInterval != "" {
		opts.CheckinInterval, err = time.ParseDuration(a.CheckinInterval)
		if err != nil {
			return err
		}
	}
	if a.Namespace == "" {
		return fmt.Errorf("--namespace or env NAMESPACE is required to be set")
	}
	if err := agent.Start(cmd.Context(), a.Kubeconfig, a.Namespace, a.AgentScope, &opts); err != nil {
		return err
	}
	<-cmd.Context().Done()
	return nil
}

func App() *cobra.Command {
	cmd := command.Command(&FleetAgent{}, cobra.Command{
		Version: version.FriendlyVersion(),
	})
	return command.AddDebug(cmd, &debugConfig)
}
