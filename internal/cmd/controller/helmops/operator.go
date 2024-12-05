package helmops

import (
	"fmt"
	"os"
	"strconv"

	"github.com/reugn/go-quartz/quartz"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	clog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/controller/helmops/reconciler"
	fcreconciler "github.com/rancher/fleet/internal/cmd/controller/reconciler"
	"github.com/rancher/fleet/internal/metrics"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/version"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
	zopts    *zap.Options
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(fleet.AddToScheme(scheme))
}

type HelmOperator struct {
	command.DebugConfig
	Kubeconfig           string `usage:"Kubeconfig file"`
	Namespace            string `usage:"namespace to watch" default:"cattle-fleet-system" env:"NAMESPACE"`
	MetricsAddr          string `name:"metrics-bind-address" default:":8081" usage:"The address the metric endpoint binds to."`
	DisableMetrics       bool   `name:"disable-metrics" usage:"Disable the metrics server."`
	EnableLeaderElection bool   `name:"leader-elect" default:"true" usage:"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager."`
	ShardID              string `usage:"only manage resources labeled with a specific shard ID" name:"shard-id"`
}

func App(zo *zap.Options) *cobra.Command {
	zopts = zo
	return command.Command(&HelmOperator{}, cobra.Command{
		Version: version.FriendlyVersion(),
		Use:     "helmops",
	})
}

// HelpFunc hides the global flag from the help output
func (c *HelmOperator) HelpFunc(cmd *cobra.Command, strings []string) {
	cmd.Parent().HelpFunc()(cmd, strings)
}

func (g *HelmOperator) PersistentPre(_ *cobra.Command, _ []string) error {
	if err := g.SetupDebug(); err != nil {
		return fmt.Errorf("failed to setup debug logging: %w", err)
	}
	zopts = g.OverrideZapOpts(zopts)

	return nil
}

func (g *HelmOperator) Run(cmd *cobra.Command, args []string) error {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(zopts)))
	ctx := clog.IntoContext(cmd.Context(), ctrl.Log.WithName("helmapp-reconciler"))

	namespace := g.Namespace

	leaderOpts, err := command.NewLeaderElectionOptions()
	if err != nil {
		return err
	}

	var shardIDSuffix string
	if g.ShardID != "" {
		shardIDSuffix = fmt.Sprintf("-%s", g.ShardID)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                  scheme,
		Metrics:                 g.setupMetrics(),
		LeaderElection:          g.EnableLeaderElection,
		LeaderElectionID:        fmt.Sprintf("fleet-helmops-leader-election-shard%s", shardIDSuffix),
		LeaderElectionNamespace: namespace,
		LeaseDuration:           leaderOpts.LeaseDuration,
		RenewDeadline:           leaderOpts.RenewDeadline,
		RetryPeriod:             leaderOpts.RetryPeriod,
	})

	if err != nil {
		return err
	}

	sched := quartz.NewStdScheduler()

	var workers int
	if d := os.Getenv("HELMOPS_RECONCILER_WORKERS"); d != "" {
		w, err := strconv.Atoi(d)
		if err != nil {
			setupLog.Error(err, "failed to parse HELMOPS_RECONCILER_WORKERS", "value", d)
		}
		workers = w
	}

	helmAppReconciler := &reconciler.HelmAppReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Scheduler: sched,
		Workers:   workers,
		ShardID:   g.ShardID,
		Recorder:  mgr.GetEventRecorderFor(fmt.Sprintf("fleet-helmops%s", shardIDSuffix)),
	}

	helmAppStatusReconciler := &reconciler.HelmAppStatusReconciler{
		Client:  mgr.GetClient(),
		Scheme:  mgr.GetScheme(),
		ShardID: g.ShardID,
		Workers: workers,
	}

	configReconciler := &fcreconciler.ConfigReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		SystemNamespace: namespace,
		ShardID:         g.ShardID,
	}

	if err := fcreconciler.Load(ctx, mgr.GetAPIReader(), namespace); err != nil {
		setupLog.Error(err, "failed to load config")
		return err
	}

	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() error {
		setupLog.Info("starting config controller")
		if err = configReconciler.SetupWithManager(mgr); err != nil {
			return err
		}

		setupLog.Info("starting gitops controller")
		if err = helmAppReconciler.SetupWithManager(mgr); err != nil {
			return err
		}

		setupLog.Info("starting helmops status controller")
		if err = helmAppStatusReconciler.SetupWithManager(mgr); err != nil {
			return err
		}

		return mgr.Start(ctx)
	})

	return group.Wait()
}

func (g *HelmOperator) setupMetrics() metricsserver.Options {
	if g.DisableMetrics {
		return metricsserver.Options{BindAddress: "0"}
	}

	metricsAddr := g.MetricsAddr
	if d := os.Getenv("HELMOPS_METRICS_BIND_ADDRESS"); d != "" {
		metricsAddr = d
	}

	metricServerOpts := metricsserver.Options{BindAddress: metricsAddr}
	metrics.RegisterHelmOpsMetrics() // enable helmops related metrics

	return metricServerOpts
}
