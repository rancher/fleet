package controller

import (
	"context"

	"github.com/reugn/go-quartz/quartz"

	"github.com/rancher/fleet/internal/cmd/controller/reconciler"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/internal/metrics"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func start(
	ctx context.Context,
	systemNamespace string,
	config *rest.Config,
	leaderOpts LeaderElectionOptions,
	bindAddresses BindAddresses,
	disableGitops bool,
	disableMetrics bool,
) error {
	setupLog.Info("listening for changes on local cluster",
		"disableGitops", disableGitops,
		"disableMetrics", disableMetrics,
	)

	var metricServerOptions metricsserver.Options
	if disableMetrics {
		metricServerOptions = metricsserver.Options{BindAddress: "0"}
	} else {
		metricServerOptions = metricsserver.Options{BindAddress: bindAddresses.Metrics}
		metrics.RegisterMetrics() // enable fleet related metrics
	}

	mgr, err := ctrl.NewManager(config, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricServerOptions,
		HealthProbeBindAddress: bindAddresses.HealthProbe,

		LeaderElection:          true,
		LeaderElectionID:        "fleet-controller-leader-election",
		LeaderElectionNamespace: systemNamespace,
		LeaseDuration:           leaderOpts.LeaseDuration,
		RenewDeadline:           leaderOpts.RenewDeadline,
		RetryPeriod:             leaderOpts.RetryPeriod,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		return err
	}

	// Set up the config reconciler
	if err = (&reconciler.ConfigReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),

		SystemNamespace: systemNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ConfigMap")
		return err
	}

	// bundle related controllers
	store := manifest.NewStore(mgr.GetClient())
	builder := target.New(mgr.GetClient())

	if err = (&reconciler.ClusterReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),

		Query: builder,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Cluster")
		return err
	}

	if err = (&reconciler.BundleReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),

		Builder: builder,
		Store:   store,
		Query:   builder,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Bundle")
		return err
	}

	sched := quartz.NewStdScheduler()

	if !disableGitops {
		if err = (&reconciler.GitRepoReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),

			Scheduler: sched,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "GitRepo")
			return err
		}
	}

	// controllers that update status.display
	if err = (&reconciler.ClusterGroupReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterGroup")
		return err
	}

	if err = (&reconciler.BundleDeploymentReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "BundleDeployment")
		return err
	}

	// imagescan controller
	if err = (&reconciler.ImageScanReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),

		Scheduler: sched,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ImageScan")
		return err
	}

	//+kubebuilder:scaffold:builder

	if err := reconciler.Load(ctx, mgr.GetAPIReader(), systemNamespace); err != nil {
		setupLog.Error(err, "failed to load config")
		return err
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		return err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		return err
	}

	setupLog.Info("starting job scheduler")
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sched.Start(jobCtx)

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		return err

	}

	sched.Stop()

	return nil
}
