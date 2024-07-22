package agent

import (
	"flag"
	"fmt"
	glog "log"
	"net/http"

	"github.com/spf13/cobra"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/pkg/version"
)

type UpstreamOptions struct {
	Kubeconfig string `usage:"kubeconfig file for agent's cluster"`
	Namespace  string `usage:"system namespace is the namespace, the agent runs in, e.g. cattle-fleet-system" env:"NAMESPACE"`
}

type FleetAgent struct {
	command.DebugConfig
	Namespace  string `usage:"system namespace is the namespace, the agent runs in, e.g. cattle-fleet-system" env:"NAMESPACE"`
	AgentScope string `usage:"An identifier used to scope the agent bundleID names, typically the same as namespace" env:"AGENT_SCOPE"`
}

var (
	setupLog = ctrl.Log.WithName("setup")
	zopts    = &zap.Options{
		Development: true,
	}
)

func (a *FleetAgent) PersistentPre(cmd *cobra.Command, _ []string) error {
	if err := a.SetupDebug(); err != nil {
		return fmt.Errorf("failed to setup debug logging: %w", err)
	}
	zopts = a.OverrideZapOpts(zopts)
	return nil
}

func (a *FleetAgent) Run(cmd *cobra.Command, args []string) error {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(zopts)))
	ctx := log.IntoContext(cmd.Context(), ctrl.Log)

	localConfig := ctrl.GetConfigOrDie()

	go func() {
		glog.Println(http.ListenAndServe("localhost:6060", nil)) // nolint:gosec // Debugging only
	}()

	if a.Namespace == "" {
		return fmt.Errorf("--namespace or env NAMESPACE is required to be set")
	}
	if err := start(ctx, localConfig, a.Namespace, a.AgentScope); err != nil {
		return err
	}

	<-cmd.Context().Done()
	return nil
}

func App() *cobra.Command {
	root := command.Command(&FleetAgent{}, cobra.Command{
		Version: version.FriendlyVersion(),
	})
	// add command line flags from zap and controller-runtime, which use
	// goflags and convert them to pflags
	fs := flag.NewFlagSet("", flag.ExitOnError)
	zopts.BindFlags(fs)
	ctrl.RegisterFlags(fs)
	root.Flags().AddGoFlagSet(fs)

	root.AddCommand(
		NewClusterStatus(),
		NewRegister(),
	)
	return root
}
