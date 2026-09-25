package agentmanagement

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
	clog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/agent"
	"github.com/rancher/fleet/pkg/version"
)

var zopts *zap.Options

type AgentManagement struct {
	command.DebugConfig
	Kubeconfig       string `usage:"kubeconfig file"`
	Namespace        string `usage:"namespace to watch" env:"NAMESPACE"`
	DisableBootstrap bool   `usage:"disable local cluster components" name:"disable-bootstrap"`
}

// HelpFunc hides the global flag from the help output
func (a *AgentManagement) HelpFunc(cmd *cobra.Command, strings []string) {
	_ = cmd.Flags().MarkHidden("disable-metrics")
	_ = cmd.Flags().MarkHidden("shard-id")
	cmd.Parent().HelpFunc()(cmd, strings)
}

func (a *AgentManagement) PersistentPre(_ *cobra.Command, _ []string) error {
	if err := a.SetupDebug(); err != nil {
		return fmt.Errorf("failed to setup debug logging: %w", err)
	}
	zopts = a.OverrideZapOpts(zopts)

	// if debug is enabled in controller, enable in agents too (unless otherwise specified)
	propagateDebug, _ := strconv.ParseBool(os.Getenv("FLEET_PROPAGATE_DEBUG_SETTINGS_TO_AGENTS"))
	if propagateDebug && a.Debug {
		agent.DebugEnabled = true
		agent.DebugLevel = a.DebugLevel
	}

	disableSecurityContext, _ := strconv.ParseBool(os.Getenv("FLEET_DEBUG_DISABLE_SECURITY_CONTEXT"))
	if propagateDebug && disableSecurityContext {
		agent.DisableSecurityContext = true
	}

	return nil
}

func (a *AgentManagement) Run(cmd *cobra.Command, args []string) error {
	if a.Namespace == "" {
		return errors.New("--namespace or env NAMESPACE is required to be set")
	}
	enforceTTL, _ := strconv.ParseBool(os.Getenv("FLEET_REGISTRATION_TOKEN_TTL_REQUIRED"))

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(zopts)))
	ctx := clog.IntoContext(cmd.Context(), ctrl.Log)

	return start(ctx, a.Kubeconfig, a.Namespace, a.DisableBootstrap, enforceTTL)
}

func App(zo *zap.Options) *cobra.Command {
	zopts = zo
	return command.Command(&AgentManagement{}, cobra.Command{
		Version: version.FriendlyVersion(),
		Use:     "agentmanagement",
	})
}
