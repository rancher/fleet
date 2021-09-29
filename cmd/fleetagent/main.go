package main

import (
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"time"

	"github.com/rancher/fleet/modules/agent/pkg/agent"
	"github.com/rancher/fleet/modules/agent/pkg/simulator"
	"github.com/rancher/fleet/pkg/version"
	command "github.com/rancher/wrangler-cli"
	"github.com/spf13/cobra"
)

var (
	debugConfig command.DebugConfig
)

type FleetAgent struct {
	Kubeconfig      string `usage:"kubeconfig file"`
	Namespace       string `usage:"namespace to watch" env:"NAMESPACE"`
	AgentScope      string `usage:"An identifier used to scope the agent bundleID names, typically the same as namespace" env:"AGENT_SCOPE"`
	Simulators      int    `usage:"Numbers of simulators to run"`
	CheckinInterval string `usage:"How often to post cluster status" env:"CHECKIN_INTERVAL"`
}

func (a *FleetAgent) Run(cmd *cobra.Command, args []string) error {
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()

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
	if a.Simulators > 0 {
		return simulator.Simulate(cmd.Context(), a.Simulators, a.Kubeconfig, a.Namespace, "default", opts)
	}
	if err := agent.Start(cmd.Context(), a.Kubeconfig, a.Namespace, a.AgentScope, &opts); err != nil {
		return err
	}
	<-cmd.Context().Done()
	return cmd.Context().Err()
}

func main() {
	cmd := command.Command(&FleetAgent{}, cobra.Command{
		Version: version.FriendlyVersion(),
	})
	cmd = command.AddDebug(cmd, &debugConfig)
	command.Main(cmd)
}
