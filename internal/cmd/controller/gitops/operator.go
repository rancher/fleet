package gitops

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/reugn/go-quartz/quartz"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/controller/gitops/reconciler"
	fcreconciler "github.com/rancher/fleet/internal/cmd/controller/reconciler"
	"github.com/rancher/fleet/internal/metrics"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/git"
	"github.com/rancher/fleet/pkg/version"
	"github.com/rancher/fleet/pkg/webhook"
)

var (
	scheme            = runtime.NewScheme()
	setupLog          = ctrl.Log.WithName("setup")
	zopts             *zap.Options
	defaultSyncPeriod = 10 * time.Hour
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(fleet.AddToScheme(scheme))
}

type GitOperator struct {
	command.DebugConfig
	Kubeconfig           string `usage:"Kubeconfig file"`
	Namespace            string `usage:"namespace to watch" default:"cattle-fleet-system" env:"NAMESPACE"`
	MetricsAddr          string `name:"metrics-bind-address" default:":8081" usage:"The address the metric endpoint binds to."`
	DisableMetrics       bool   `name:"disable-metrics" usage:"Disable the metrics server."`
	EnableLeaderElection bool   `name:"leader-elect" default:"true" usage:"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager."`
	Image                string `name:"gitjob-image" default:"rancher/fleet:dev" usage:"The gitjob image that will be used in the generated job."`
	Listen               string `default:":8080" usage:"The port the webhook listens."`
	ShardID              string `usage:"only manage resources labeled with a specific shard ID" name:"shard-id"`
	ShardNodeSelector    string `usage:"node selector to apply to jobs based on the shard ID, if any" name:"shard-node-selector"`
}

func App(zo *zap.Options) *cobra.Command {
	zopts = zo
	return command.Command(&GitOperator{}, cobra.Command{
		Version: version.FriendlyVersion(),
		Use:     "gitjob",
	})
}

// HelpFunc hides the global flag from the help output
func (c *GitOperator) HelpFunc(cmd *cobra.Command, strings []string) {
	cmd.Parent().HelpFunc()(cmd, strings)
}

func (g *GitOperator) PersistentPre(_ *cobra.Command, _ []string) error {
	if err := g.SetupDebug(); err != nil {
		return fmt.Errorf("failed to setup debug logging: %w", err)
	}
	zopts = g.OverrideZapOpts(zopts)

	return nil
}

func (g *GitOperator) Run(cmd *cobra.Command, args []string) error {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(zopts)))
	ctx := clog.IntoContext(cmd.Context(), ctrl.Log.WithName("gitjob-reconciler"))

	namespace := g.Namespace

	leaderOpts, err := command.NewLeaderElectionOptions()
	if err != nil {
		return err
	}

	var shardIDSuffix string
	if g.ShardID != "" {
		shardIDSuffix = fmt.Sprintf("-%s", g.ShardID)
	}

	syncPeriod := defaultSyncPeriod
	if d := os.Getenv("GITREPO_SYNC_PERIOD"); d != "" {
		syncPeriod, err = time.ParseDuration(d)
		if err != nil {
			return err
		}
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                  scheme,
		Metrics:                 g.setupMetrics(),
		LeaderElection:          g.EnableLeaderElection,
		LeaderElectionID:        fmt.Sprintf("fleet-gitops-leader-election-shard%s", shardIDSuffix),
		LeaderElectionNamespace: namespace,
		LeaseDuration:           leaderOpts.LeaseDuration,
		RenewDeadline:           leaderOpts.RenewDeadline,
		RetryPeriod:             leaderOpts.RetryPeriod,
		// resync to pick up lost gitrepos
		Cache: cache.Options{
			SyncPeriod: &syncPeriod,
		},
	})

	if err != nil {
		return err
	}

	sched := quartz.NewStdScheduler()

	var workers int
	if d := os.Getenv("GITREPO_RECONCILER_WORKERS"); d != "" {
		w, err := strconv.Atoi(d)
		if err != nil {
			setupLog.Error(err, "failed to parse GITREPO_RECONCILER_WORKERS", "value", d)
		}
		workers = w
	}

	gitJobReconciler := &reconciler.GitJobReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		Image:           g.Image,
		Scheduler:       sched,
		Workers:         workers,
		ShardID:         g.ShardID,
		JobNodeSelector: g.ShardNodeSelector,
		GitFetcher:      &git.Fetch{},
		Clock:           reconciler.RealClock{},
		Recorder:        mgr.GetEventRecorderFor(fmt.Sprintf("fleet-gitops%s", shardIDSuffix)),
		SystemNamespace: namespace,
	}

	statusReconciler := &reconciler.StatusReconciler{
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
		return startWebhook(ctx, namespace, g.Listen, mgr.GetClient(), mgr.GetCache())
	})
	group.Go(func() error {
		setupLog.Info("starting config controller")
		if err = configReconciler.SetupWithManager(mgr); err != nil {
			return err
		}

		setupLog.Info("starting gitops controller")
		if err = gitJobReconciler.SetupWithManager(mgr); err != nil {
			return err
		}

		setupLog.Info("starting gitops status controller")
		if err = statusReconciler.SetupWithManager(mgr); err != nil {
			return err
		}

		return mgr.Start(ctx)
	})

	return group.Wait()
}

func (g *GitOperator) setupMetrics() metricsserver.Options {
	if g.DisableMetrics {
		return metricsserver.Options{BindAddress: "0"}
	}

	metricsAddr := g.MetricsAddr
	if d := os.Getenv("GITOPS_METRICS_BIND_ADDRESS"); d != "" {
		metricsAddr = d
	}

	metricServerOpts := metricsserver.Options{BindAddress: metricsAddr}
	metrics.RegisterGitOptsMetrics() // enable gitops related metrics

	return metricServerOpts
}

func startWebhook(ctx context.Context, namespace string, addr string, client client.Client, cacheClient cache.Cache) error {
	setupLog.Info("Setting up webhook listener")
	handler, err := webhook.HandleHooks(ctx, namespace, client, cacheClient)
	if err != nil {
		return fmt.Errorf("webhook handler can't be created: %w", err)
	}
	server := &http.Server{
		Addr:    addr,
		Handler: handler,
		// According to https://blog.cloudflare.com/the-complete-guide-to-golang-net-http-timeouts/
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	return nil
}
