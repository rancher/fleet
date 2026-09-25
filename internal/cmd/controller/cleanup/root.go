package cleanup

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
	clog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/pkg/version"
)

var zopts *zap.Options

type CleanUp struct {
	command.DebugConfig
	Kubeconfig string `usage:"kubeconfig file"`
	Namespace  string `usage:"namespace to watch" env:"NAMESPACE"`
}

// HelpFunc hides the global flags from the help output
func (c *CleanUp) HelpFunc(cmd *cobra.Command, strings []string) {
	_ = cmd.Flags().MarkHidden("disable-metrics")
	_ = cmd.Flags().MarkHidden("shard-id")
	cmd.Parent().HelpFunc()(cmd, strings)
}

func (c *CleanUp) PersistentPre(_ *cobra.Command, _ []string) error {
	if err := c.SetupDebug(); err != nil {
		return fmt.Errorf("failed to setup debug logging: %w", err)
	}
	zopts = c.OverrideZapOpts(zopts)

	return nil
}

func (c *CleanUp) Run(cmd *cobra.Command, args []string) error {
	if c.Namespace == "" {
		return errors.New("--namespace or env NAMESPACE is required to be set")
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(zopts)))
	ctx := clog.IntoContext(cmd.Context(), ctrl.Log)

	return start(ctx, c.Kubeconfig, c.Namespace)
}

func App(zo *zap.Options) *cobra.Command {
	zopts = zo
	return command.Command(&CleanUp{}, cobra.Command{
		Version: version.FriendlyVersion(),
		Use:     "cleanup",
	})
}
