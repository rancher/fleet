package agent

import (
	"context"
	"fmt"
	glog "log"
	"os"

	"github.com/spf13/cobra"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

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
	zopts = r.OverrideZapOpts(zopts)
	return nil
}

func (r *Register) Run(cmd *cobra.Command, args []string) error {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(zopts)))
	ctx := log.IntoContext(cmd.Context(), ctrl.Log)

	clientConfig := kubeconfig.GetNonInteractiveClientConfig(r.Kubeconfig)
	kc, err := clientConfig.ClientConfig()
	if err != nil {
		return err
	}
	glog.Printf("client config: %v", kc)

	localClient, err := kubernetes.NewForConfig(kc)
	if err != nil {
		return fmt.Errorf("failed to create local client: %w", err)
	}

	identifier, err := getUniqueIdentifier()
	if err != nil {
		return fmt.Errorf("failed to get unique identifier: %w", err)
	}

	lock := resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      leaseLockNameRegister,
			Namespace: r.Namespace,
		},
		Client: localClient.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: identifier,
		},
	}

	leaderOpts, err := command.NewLeaderElectionOptions()
	if err != nil {
		return err
	}
	glog.Println("leaderOpts", leaderOpts)

	leaderElectionConfig := leaderelection.LeaderElectionConfig{
		Lock:          &lock,
		LeaseDuration: *leaderOpts.LeaseDuration,
		RetryPeriod:   *leaderOpts.RetryPeriod,
		RenewDeadline: *leaderOpts.RenewDeadline,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				setupLog.Info("starting registration on upstream cluster", "namespace", r.Namespace)

				ctx, cancel := context.WithCancel(ctx)
				defer cancel()

				// try to register with upstream fleet controller by obtaining
				// a kubeconfig for the upstream cluster
				agentInfo, err := register.Register(ctx, r.Namespace, kc)
				if err != nil {
					setupLog.Error(err, "failed to register with upstream cluster")
					return
				}

				ns, _, err := agentInfo.ClientConfig.Namespace()
				if err != nil {
					setupLog.Error(err, "failed to get namespace from upstream cluster")
					return
				}

				_, err = agentInfo.ClientConfig.ClientConfig()
				if err != nil {
					setupLog.Error(err, "failed to get kubeconfig from upstream cluster")
					return
				}

				setupLog.Info("successfully registered with upstream cluster", "namespace", ns)
				os.Exit(0)
			},
			OnStoppedLeading: func() {
				setupLog.Info("stopped leading")
				os.Exit(1)
			},
			OnNewLeader: func(identity string) {
				if identity == identifier {
					setupLog.Info("renewed leader", "identity", identity)
				} else {
					setupLog.Info("new leader", "identity", identity)
				}
			},
		},
	}

	leaderelection.RunOrDie(ctx, leaderElectionConfig)

	return nil
}
