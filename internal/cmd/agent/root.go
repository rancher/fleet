package agent

import (
	"context"
	"flag"
	"fmt"
	glog "log"
	"net/http"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

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

type AgentReconcilerWorkers struct {
	BundleDeployment int
	Drift            int
}

const (
	leaseLockName              = "fleet-agent"
	leaseLockNameClusterStatus = "fleet-agent-clusterstatus"
	leaseLockNameRegister      = "fleet-agent-register"
)

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

func getUniqueIdentifier() (string, error) {
	// For in-cluster deployments.
	if podName, ok := os.LookupEnv("POD_NAME"); ok {
		return podName, nil
	}

	// For local development, combine hostname with process ID
	hostname, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("failed to get hostname: %w", err)
	}

	pid := os.Getpid()
	return fmt.Sprintf("%s-%d", hostname, pid), nil
}

func (a *FleetAgent) Run(cmd *cobra.Command, args []string) error {
	logger := zap.New(zap.UseFlagOptions(zopts))
	ctrl.SetLogger(logger)

	if a.Namespace == "" {
		return fmt.Errorf("--namespace or env NAMESPACE is required to be set")
	}

	ctx := log.IntoContext(cmd.Context(), ctrl.Log)

	localConfig := ctrl.GetConfigOrDie()
	localClient, err := kubernetes.NewForConfig(localConfig)
	if err != nil {
		return fmt.Errorf("failed to create local client: %w", err)
	}

	identifier, err := getUniqueIdentifier()
	if err != nil {
		return fmt.Errorf("failed to get unique identifier: %w", err)
	}

	lock := resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      leaseLockName,
			Namespace: a.Namespace,
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

	go func() {
		glog.Println(http.ListenAndServe("localhost:6060", nil)) // nolint:gosec // Debugging only
	}()

	leaderElectionConfig := leaderelection.LeaderElectionConfig{
		Lock:          &lock,
		LeaseDuration: *leaderOpts.LeaseDuration,
		RetryPeriod:   *leaderOpts.RetryPeriod,
		RenewDeadline: *leaderOpts.RenewDeadline,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				workersOpts := AgentReconcilerWorkers{}

				if d := os.Getenv("BUNDLEDEPLOYMENT_RECONCILER_WORKERS"); d != "" {
					w, err := strconv.Atoi(d)
					if err != nil {
						setupLog.Error(err, "failed to parse BUNDLEDEPLOYMENT_RECONCILER_WORKERS", "value", d)
					}
					workersOpts.BundleDeployment = w
				}

				if d := os.Getenv("DRIFT_RECONCILER_WORKERS"); d != "" {
					w, err := strconv.Atoi(d)
					if err != nil {
						setupLog.Error(err, "failed to parse DRIFT_RECONCILER_WORKERS", "value", d)
					}
					workersOpts.Drift = w
				}
				if err := start(ctx, localConfig, a.Namespace, a.AgentScope, workersOpts); err != nil {
					setupLog.Error(err, "failed to start agent")
				}
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
