//go:generate bash ./scripts/controller-gen-generate.sh

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	gitjobv1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	"github.com/rancher/gitjob/pkg/controller"
	"github.com/rancher/gitjob/pkg/git/poll"
	"github.com/rancher/gitjob/pkg/webhook"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"golang.org/x/sync/errgroup"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(gitjobv1.AddToScheme(scheme))
}

type flags struct {
	metricsAddr          string
	enableLeaderElection bool
	image                string
	listen               string
	debug                bool
}

func main() {
	ctx := ctrl.SetupSignalHandler()
	if err := run(ctx); err != nil {
		setupLog.Error(err, "error running gitjob")
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	namespace := os.Getenv("NAMESPACE")
	flags := bindFlags()
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: flags.metricsAddr,
		},
		LeaderElection:          flags.enableLeaderElection,
		LeaderElectionID:        "gitjob-leader",
		LeaderElectionNamespace: namespace,
	})
	if err != nil {
		return err
	}
	reconciler := &controller.GitJobReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Image:     flags.image,
		GitPoller: poll.NewHandler(mgr.GetClient()),
		Log:       ctrl.Log.WithName("gitjob-reconciler"),
	}

	group := errgroup.Group{}
	group.Go(func() error {
		return startWebhook(ctx, namespace, flags.listen, mgr.GetClient(), mgr.GetCache())
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

func bindFlags() flags {
	var metricsAddr string
	var enableLeaderElection bool
	var image string
	var listen string
	var debug bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8081", "The address the metric endpoint binds to.")
	flag.StringVar(&image, "gitjob-image", "rancher/gitjob:dev", "The gitjob image that will be used in the generated job.")
	flag.StringVar(&listen, "listen", ":8080", "The port the webhook listens.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&debug, "debug", false, "debug mode.")
	opts := zap.Options{
		Development: debug,
	}
	opts.BindFlags(flag.CommandLine)
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	flag.Parse()

	return flags{
		metricsAddr:          metricsAddr,
		enableLeaderElection: enableLeaderElection,
		image:                image,
		listen:               listen,
		debug:                debug,
	}
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
		server.Shutdown(shutdownCtx)
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	return nil
}
