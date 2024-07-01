package agent

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/agent/register"

	"github.com/rancher/wrangler/v3/pkg/kubeconfig"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func NewRegister() *cobra.Command {
	cmd := command.Command(&Register{}, cobra.Command{
		Use:   "register [flags]",
		Short: "Register agent with an upstream cluster",
	})
	return cmd
}

type Register struct {
	command.DebugConfig
	UpstreamOptions
}

// HelpFunc hides the global agent-scope flag from the help output
func (c *Register) HelpFunc(cmd *cobra.Command, strings []string) {
	_ = cmd.Flags().MarkHidden("agent-scope")
	cmd.Parent().HelpFunc()(cmd, strings)
}

func (r *Register) PersistentPre(cmd *cobra.Command, _ []string) error {
	if err := r.SetupDebug(); err != nil {
		return fmt.Errorf("failed to setup debug logging: %w", err)
	}
	return nil
}

func (r *Register) Run(cmd *cobra.Command, args []string) error {
	zopts.Development = r.Debug
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))
	ctx := log.IntoContext(cmd.Context(), ctrl.Log)

	clientConfig := kubeconfig.GetNonInteractiveClientConfig(r.Kubeconfig)
	kc, err := clientConfig.ClientConfig()
	if err != nil {
		return err
	}

	setupLog.Info("starting registration on upstream cluster", "namespace", r.Namespace)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// try to register with upstream fleet controller by obtaining
	// a kubeconfig for the upstream cluster
	agentInfo, err := register.Register(ctx, r.Namespace, kc)
	if err != nil {
		setupLog.Error(err, "failed to register with upstream cluster")
		return err
	}

	ns, _, err := agentInfo.ClientConfig.Namespace()
	if err != nil {
		setupLog.Error(err, "failed to get namespace from upstream cluster")
		return err
	}

	_, err = agentInfo.ClientConfig.ClientConfig()
	if err != nil {
		setupLog.Error(err, "failed to get kubeconfig from upstream cluster")
		return err
	}

	setupLog.Info("successfully registered with upstream cluster", "namespace", ns)

	return nil
}
