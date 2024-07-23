package gitops

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/controller/gitops/reconciler"
	"github.com/rancher/fleet/internal/metrics"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/git"
	"github.com/rancher/fleet/pkg/version"
	"github.com/rancher/fleet/pkg/webhook"
	"github.com/reugn/go-quartz/quartz"
	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"golang.org/x/sync/errgroup"
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
	_ = cmd.Flags().MarkHidden("disable-gitops")
	_ = cmd.Flags().MarkHidden("disable-metrics")
	_ = cmd.Flags().MarkHidden("shard-id")
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

	var leaderElectionSuffix string
	if g.ShardID != "" {
		leaderElectionSuffix = fmt.Sprintf("-%s", g.ShardID)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                  scheme,
		Metrics:                 g.setupMetrics(),
		LeaderElection:          g.EnableLeaderElection,
		LeaderElectionID:        fmt.Sprintf("fleet-gitops-leader-election-shard%s", leaderElectionSuffix),
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
	if d := os.Getenv("GITREPO_RECONCILER_WORKERS"); d != "" {
		w, err := strconv.Atoi(d)
		if err != nil {
			setupLog.Error(err, "failed to parse GITREPO_RECONCILER_WORKERS", "value", d)
		}
		workers = w
	}

	reconciler := &reconciler.GitJobReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		Image:      g.Image,
		Scheduler:  sched,
		Workers:    workers,
		ShardID:    g.ShardID,
		GitFetcher: &git.Fetch{},
		Clock:      reconciler.RealClock{},
	}

	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() error {
		return startWebhook(ctx, namespace, g.Listen, mgr.GetClient(), mgr.GetCache())
	})
	group.Go(func() error {
		setupLog.Info("starting manager")

		if err = reconciler.SetupWithManager(mgr); err != nil {
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
