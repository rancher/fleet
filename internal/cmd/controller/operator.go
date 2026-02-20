package controller

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/reugn/go-quartz/quartz"

	"github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/controller/reconciler"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/experimental"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/internal/metrics"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(fleet.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func start(
	ctx context.Context,
	systemNamespace string,
	config *rest.Config,
	leaderElection bool,
	leaderOpts cmd.LeaderElectionOptions,
	workersOpts ControllerReconcilerWorkers,
	bindAddresses BindAddresses,
	disableMetrics bool,
	shardID string,
) error {
	setupLog.Info("listening for changes on local cluster",
		"disableMetrics", disableMetrics,
	)

	var metricServerOptions metricsserver.Options
	if disableMetrics {
		metricServerOptions = metricsserver.Options{BindAddress: "0"}
	} else {
		metricServerOptions = metricsserver.Options{BindAddress: bindAddresses.Metrics}
		metrics.RegisterMetrics()
	}

	var leaderElectionSuffix string
	if shardID != "" {
		leaderElectionSuffix = fmt.Sprintf("-%s", shardID)
	}

	mgr, err := ctrl.NewManager(config, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricServerOptions,
		HealthProbeBindAddress: bindAddresses.HealthProbe,

		LeaderElection:          leaderElection,
		LeaderElectionID:        fmt.Sprintf("fleet-controller-leader-election-shard%s", leaderElectionSuffix),
		LeaderElectionNamespace: systemNamespace,
		LeaseDuration:           &leaderOpts.LeaseDuration,
		RenewDeadline:           &leaderOpts.RenewDeadline,
		RetryPeriod:             &leaderOpts.RetryPeriod,
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
		ShardID:         shardID,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ConfigMap")
		return err
	}

	// bundle related controllers
	store := manifest.NewStore(mgr.GetClient())
	builder := target.New(mgr.GetClient(), mgr.GetAPIReader())

	if err = (&reconciler.ClusterReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),

		Query:   builder,
		ShardID: shardID,

		Workers: workersOpts.Cluster,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Cluster")
		return err
	}

	var shardIDSuffix string
	if shardID != "" {
		shardIDSuffix = fmt.Sprintf("-%s", shardID)
	}
	if err = (&reconciler.BundleReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor(fmt.Sprintf("fleet-bundle-ctrl%s", shardIDSuffix)),

		Builder: builder,
		Store:   store,
		Query:   builder,
		ShardID: shardID,

		Workers: workersOpts.Bundle,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Bundle")
		return err
	}

	sched, err := quartz.NewStdScheduler()
	if err != nil {
		return fmt.Errorf("failed to create scheduler: %w", err)
	}

	// controllers that update status.display
	if err = (&reconciler.ClusterGroupReconciler{
		Client:  mgr.GetClient(),
		Scheme:  mgr.GetScheme(),
		ShardID: shardID,

		Workers: workersOpts.ClusterGroup,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterGroup")
		return err
	}

	if err = (&reconciler.BundleDeploymentReconciler{
		Client:  mgr.GetClient(),
		Scheme:  mgr.GetScheme(),
		ShardID: shardID,

		Workers: workersOpts.BundleDeployment,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "BundleDeployment")
		return err
	}

	imagescanEnabled := false
	if v := os.Getenv("IMAGESCAN_ENABLED"); v != "" {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			setupLog.Error(err, "failed to parse IMAGESCAN_ENABLED", "value", v)
		}
		imagescanEnabled = enabled
	}

	if imagescanEnabled {
		if err = (&reconciler.ImageScanReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),

			Scheduler: sched,
			ShardID:   shardID,

			Workers: workersOpts.ImageScan,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "ImageScan")
			return err
		}
	}

	if experimental.SchedulesEnabled() {
		if err = (&reconciler.ScheduleReconciler{
			Client:   mgr.GetClient(),
			Scheme:   mgr.GetScheme(),
			Recorder: mgr.GetEventRecorderFor(fmt.Sprintf("fleet-schedule-ctrl%s", shardIDSuffix)),
			ShardID:  shardID,

			Workers:   workersOpts.Schedule,
			Scheduler: sched,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "Schedule")
			return err
		}
	}

	// Add an indexer for the ContentName label as that will make accesses in the cache
	// faster
	if err := AddContentNameLabelIndexer(ctx, mgr); err != nil {
		return err
	}

	// Add an indexer for Bundle DownstreamResources (secrets and configmaps)
	if err := AddBundleDownstreamResourceIndexer(ctx, mgr); err != nil {
		return err
	}

	if err = (&reconciler.ContentReconciler{
		Client:  mgr.GetClient(),
		Scheme:  mgr.GetScheme(),
		ShardID: shardID,
		Workers: workersOpts.Content,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Content")
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

func AddContentNameLabelIndexer(ctx context.Context, mgr manager.Manager) error {
	return mgr.GetFieldIndexer().IndexField(
		ctx,
		&fleet.BundleDeployment{},
		config.ContentNameIndex,
		func(obj client.Object) []string {
			content, ok := obj.(*fleet.BundleDeployment)
			if !ok {
				return nil
			}
			if val, exists := content.Labels[fleet.ContentNameLabel]; exists {
				return []string{val}
			}
			return nil
		},
	)
}

// AddBundleDownstreamResourceIndexer indexes Bundles by their DownstreamResources (secrets and configmaps).
// This allows querying which bundles reference a specific secret or configmap, enabling reconciliation
// when those resources change.
func AddBundleDownstreamResourceIndexer(ctx context.Context, mgr manager.Manager) error {
	return mgr.GetFieldIndexer().IndexField(
		ctx,
		&fleet.Bundle{},
		config.BundleDownstreamResourceIndex,
		func(obj client.Object) []string {
			bundle, ok := obj.(*fleet.Bundle)
			if !ok {
				return nil
			}

			// Extract all downstream resource names (secrets and configmaps)
			var resources []string
			for _, dr := range bundle.Spec.DownstreamResources {
				lowerKind := strings.ToLower(dr.Kind)
				if lowerKind == "secret" || lowerKind == "configmap" {
					// Index by "Kind/Name" to uniquely identify the resource
					resources = append(resources, fmt.Sprintf("%s/%s", lowerKind, dr.Name))
				}
			}
			return resources
		},
	)
}
