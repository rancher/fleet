package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/reugn/go-quartz/quartz"

	"github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/controller/reconciler"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/internal/metrics"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/rancher/fleet/internal/cmd/agent/deployer/monitor"
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
		metrics.RegisterMetrics() // enable fleet related metrics
	}

	var leaderElectionSuffix string
	if shardID != "" {
		leaderElectionSuffix = fmt.Sprintf("-%s", shardID)
	}

	mgr, err := ctrl.NewManager(config, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricServerOptions,
		HealthProbeBindAddress: bindAddresses.HealthProbe,

		LeaderElection:          true,
		LeaderElectionID:        fmt.Sprintf("fleet-controller-leader-election-shard%s", leaderElectionSuffix),
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
		ShardID:         shardID,
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

		Query:   builder,
		ShardID: shardID,
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
		ShardID: shardID,

		Workers: workersOpts.Bundle,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Bundle")
		return err
	}

	sched := quartz.NewStdScheduler()

	// controllers that update status.display
	if err = (&reconciler.ClusterGroupReconciler{
		Client:  mgr.GetClient(),
		Scheme:  mgr.GetScheme(),
		ShardID: shardID,
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

	// imagescan controller
	if err = (&reconciler.ImageScanReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),

		Scheduler: sched,
		ShardID:   shardID,
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

	setupLog.Info("starting cluster status monitor")
	go runClusterStatusMonitor(ctx, mgr.GetClient())

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

func runClusterStatusMonitor(ctx context.Context, c client.Client) {
	threshold := 15 * time.Second // TODO load or hard-code sensible value

	logger := ctrl.Log.WithName("cluster status monitor")

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(threshold):
		}

		clusters := &v1alpha1.ClusterList{}
		if err := c.List(ctx, clusters); err != nil {
			logger.Error(err, "Failed to get list of clusters")
			continue
		}

		for _, cluster := range clusters.Items {
			lastSeen := cluster.Status.Agent.LastSeen

			// FIXME threshold should not be lower than cluster status refresh default value (15 min)

			// XXX: should the same value be used for both the polling interval and the threshold?
			logger.Info("Checking cluster status", "cluster", cluster.Name, "last seen", lastSeen.UTC().String())

			// XXX: do we want to run this more than once per cluster, updating the timestamp each time?
			// Or would it make sense to keep the oldest possible timestamp in place, for users to know since when the
			// cluster is offline?

			// lastSeen being 0 would typically mean that the cluster is not registered yet, in which case bundle
			// deployments should not be deployed there.
			if lastSeen.IsZero() || time.Now().UTC().Sub(lastSeen.UTC()) < threshold {
				continue
			}

			logger.Info("Detected offline cluster", "cluster", cluster.Name)

			// Cluster is offline
			bundleDeployments := &v1alpha1.BundleDeploymentList{}
			if err := c.List(ctx, bundleDeployments, client.InNamespace(cluster.Status.Namespace)); err != nil {
				logger.Error(
					err,
					"Failed to get list of bundle deployments for offline cluster",
					"cluster",
					cluster.Name,
					"namespace",
					cluster.Status.Namespace,
				)
				continue
			}

			// These updates should not conflict with those done by the bundle deployment reconciler (offline vs online
			// clusters).
			for _, bd := range bundleDeployments.Items {
				logger.Info("Updating bundle deployment in offline cluster", "cluster", cluster.Name, "bundledeployment", bd.Name)
				err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
					t := &v1alpha1.BundleDeployment{}
					nsn := types.NamespacedName{Name: bd.Name, Namespace: bd.Namespace}
					logger.Info("[DEBUG] getting bundle deployment", "cluster", cluster.Name, "bundledeployment", bd.Name)
					if err := c.Get(ctx, nsn, t); err != nil {
						return err
					}
					t.Status = bd.Status
					// TODO status updates: update condition with type Ready
					t.Status.ModifiedStatus = nil

					for _, cond := range bd.Status.Conditions {
						if cond.Type != "Ready" {
							continue
						}

						// FIXME: avoid relying on agent pkg for this?
						mc := monitor.Cond(v1alpha1.BundleDeploymentConditionReady)
						mc.SetError(&bd.Status, "Cluster offline", fmt.Errorf("cluster is offline"))
						//cond.LastUpdated(status, time.Now().UTC().Format(time.RFC3339))
					}

					logger.Info("[DEBUG] updating bundle deployment status", "cluster", cluster.Name, "bundledeployment", bd.Name)

					return c.Status().Update(ctx, t)
				})
				if err != nil {
					logger.Error(
						err,
						"Failed to update bundle deployment status for offline cluster",
						"bundledeployment",
						bd.Name,
						"cluster",
						cluster.Name,
						"namespace",
						cluster.Status.Namespace,
					)
				}
			}
		}
	}
}
