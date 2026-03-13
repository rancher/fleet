package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/monitor/reconciler"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

// ControllerLogConfig holds logging configuration for a single controller
type ControllerLogConfig struct {
	Detailed       bool                        // true = detailed logs, false = summary only
	EventFilters   reconciler.EventTypeFilters // Which event types to show in detailed mode
	ResourceFilter *reconciler.ResourceFilter  // Which resources to monitor (namespace/name patterns)
}

// ControllerLoggingConfig holds logging configuration for all controllers
type ControllerLoggingConfig struct {
	Bundle           ControllerLogConfig
	BundleDeployment ControllerLogConfig
	Cluster          ControllerLogConfig
	GitRepo          ControllerLogConfig
	HelmOp           ControllerLogConfig
}

type MonitorOptions struct {
	EnableBundle           bool
	EnableBundleDeployment bool
	EnableCluster          bool
	EnableGitRepo          bool
	EnableHelmOp           bool
	Workers                MonitorReconcilerWorkers

	// Per-controller logging configuration
	ControllerLogging ControllerLoggingConfig

	// Summary configuration
	SummaryInterval time.Duration
	SummaryReset    bool
}

func start(
	ctx context.Context,
	systemNamespace string,
	config *rest.Config,
	leaderOpts cmd.LeaderElectionOptions,
	monitorOpts MonitorOptions,
	shardID string,
) error {
	// Compile resource filters and check for errors
	if err := compileResourceFilters(&monitorOpts.ControllerLogging); err != nil {
		return fmt.Errorf("invalid resource filter configuration: %w", err)
	}

	setupLog.Info("starting fleet monitor",
		"namespace", systemNamespace,
		"shardID", shardID,
		"enableBundle", monitorOpts.EnableBundle,
		"enableBundleDeployment", monitorOpts.EnableBundleDeployment,
		"enableCluster", monitorOpts.EnableCluster,
		"enableGitRepo", monitorOpts.EnableGitRepo,
		"enableHelmOp", monitorOpts.EnableHelmOp,
		"bundleDetailedLogs", monitorOpts.ControllerLogging.Bundle.Detailed,
		"bundleDeploymentDetailedLogs", monitorOpts.ControllerLogging.BundleDeployment.Detailed,
		"clusterDetailedLogs", monitorOpts.ControllerLogging.Cluster.Detailed,
		"gitRepoDetailedLogs", monitorOpts.ControllerLogging.GitRepo.Detailed,
		"helmOpDetailedLogs", monitorOpts.ControllerLogging.HelmOp.Detailed,
		"summaryInterval", monitorOpts.SummaryInterval,
		"summaryReset", monitorOpts.SummaryReset,
	)

	// Log resource filter configuration if any filters are set
	logResourceFilters(&monitorOpts.ControllerLogging)

	// Start summary printer (always runs, prints stats for all controllers)
	go startSummaryPrinter(ctx, monitorOpts.SummaryInterval, monitorOpts.SummaryReset)

	// No metrics for monitoring controllers
	metricServerOptions := metricsserver.Options{BindAddress: "0"}

	var leaderElectionSuffix string
	if shardID != "" {
		leaderElectionSuffix = fmt.Sprintf("-%s", shardID)
	}

	mgr, err := ctrl.NewManager(config, ctrl.Options{
		Scheme:                  scheme,
		Metrics:                 metricServerOptions,
		HealthProbeBindAddress:  "0", // No health probes
		LeaderElection:          true,
		LeaderElectionID:        fmt.Sprintf("fleet-event-monitor-leader-election-shard%s", leaderElectionSuffix),
		LeaderElectionNamespace: systemNamespace,
		LeaseDuration:           &leaderOpts.LeaseDuration,
		RenewDeadline:           &leaderOpts.RenewDeadline,
		RetryPeriod:             &leaderOpts.RetryPeriod,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		return err
	}

	// Add field indexers required by the monitor controllers
	if monitorOpts.EnableBundle {
		if err := addBundleDownstreamResourceIndexer(ctx, mgr); err != nil {
			setupLog.Error(err, "unable to add Bundle downstream resource indexer")
			return err
		}
	}

	if monitorOpts.EnableGitRepo {
		if err := addGitRepoSecretIndexers(ctx, mgr); err != nil {
			setupLog.Error(err, "unable to add GitRepo secret indexers")
			return err
		}
	}

	// Register enabled monitor controllers with per-controller logging mode
	if monitorOpts.EnableBundle {
		if err := (&reconciler.BundleMonitorReconciler{
			Client:         mgr.GetClient(),
			Scheme:         mgr.GetScheme(),
			ShardID:        shardID,
			Workers:        monitorOpts.Workers.Bundle,
			Query:          reconciler.NewBundleQuery(mgr.GetClient()),
			DetailedLogs:   monitorOpts.ControllerLogging.Bundle.Detailed,
			EventFilters:   monitorOpts.ControllerLogging.Bundle.EventFilters,
			ResourceFilter: monitorOpts.ControllerLogging.Bundle.ResourceFilter,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create monitor controller", "controller", "Bundle")
			return err
		}
		setupLog.Info("registered monitor controller", "controller", "Bundle", "workers", monitorOpts.Workers.Bundle, "mode", reconciler.LogMode(monitorOpts.ControllerLogging.Bundle.Detailed))
	}

	if monitorOpts.EnableCluster {
		if err := (&reconciler.ClusterMonitorReconciler{
			Client:         mgr.GetClient(),
			Scheme:         mgr.GetScheme(),
			ShardID:        shardID,
			Workers:        monitorOpts.Workers.Cluster,
			DetailedLogs:   monitorOpts.ControllerLogging.Cluster.Detailed,
			EventFilters:   monitorOpts.ControllerLogging.Cluster.EventFilters,
			ResourceFilter: monitorOpts.ControllerLogging.Cluster.ResourceFilter,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create monitor controller", "controller", "Cluster")
			return err
		}
		setupLog.Info("registered monitor controller", "controller", "Cluster", "workers", monitorOpts.Workers.Cluster, "mode", reconciler.LogMode(monitorOpts.ControllerLogging.Cluster.Detailed))
	}

	if monitorOpts.EnableBundleDeployment {
		if err := (&reconciler.BundleDeploymentMonitorReconciler{
			Client:         mgr.GetClient(),
			Scheme:         mgr.GetScheme(),
			ShardID:        shardID,
			Workers:        monitorOpts.Workers.BundleDeployment,
			DetailedLogs:   monitorOpts.ControllerLogging.BundleDeployment.Detailed,
			EventFilters:   monitorOpts.ControllerLogging.BundleDeployment.EventFilters,
			ResourceFilter: monitorOpts.ControllerLogging.BundleDeployment.ResourceFilter,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create monitor controller", "controller", "BundleDeployment")
			return err
		}
		setupLog.Info("registered monitor controller", "controller", "BundleDeployment", "workers", monitorOpts.Workers.BundleDeployment, "mode", reconciler.LogMode(monitorOpts.ControllerLogging.BundleDeployment.Detailed))
	}

	if monitorOpts.EnableGitRepo {
		if err := (&reconciler.GitRepoMonitorReconciler{
			Client:         mgr.GetClient(),
			Scheme:         mgr.GetScheme(),
			ShardID:        shardID,
			Workers:        monitorOpts.Workers.GitRepo,
			DetailedLogs:   monitorOpts.ControllerLogging.GitRepo.Detailed,
			EventFilters:   monitorOpts.ControllerLogging.GitRepo.EventFilters,
			ResourceFilter: monitorOpts.ControllerLogging.GitRepo.ResourceFilter,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create monitor controller", "controller", "GitRepo")
			return err
		}
		setupLog.Info("registered monitor controller", "controller", "GitRepo", "workers", monitorOpts.Workers.GitRepo, "mode", reconciler.LogMode(monitorOpts.ControllerLogging.GitRepo.Detailed))
	}

	if monitorOpts.EnableHelmOp {
		if err := (&reconciler.HelmOpMonitorReconciler{
			Client:         mgr.GetClient(),
			Scheme:         mgr.GetScheme(),
			ShardID:        shardID,
			Workers:        monitorOpts.Workers.HelmOp,
			DetailedLogs:   monitorOpts.ControllerLogging.HelmOp.Detailed,
			EventFilters:   monitorOpts.ControllerLogging.HelmOp.EventFilters,
			ResourceFilter: monitorOpts.ControllerLogging.HelmOp.ResourceFilter,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create monitor controller", "controller", "HelmOp")
			return err
		}
		setupLog.Info("registered monitor controller", "controller", "HelmOp", "workers", monitorOpts.Workers.HelmOp, "mode", reconciler.LogMode(monitorOpts.ControllerLogging.HelmOp.Detailed))
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		return err
	}

	return nil
}

// compileResourceFilters compiles all resource filter regex patterns
// Returns error if any pattern is invalid
func compileResourceFilters(cfg *ControllerLoggingConfig) error {
	if err := cfg.Bundle.ResourceFilter.Compile(); err != nil {
		return fmt.Errorf("bundle resource filter: %w", err)
	}
	if err := cfg.BundleDeployment.ResourceFilter.Compile(); err != nil {
		return fmt.Errorf("bundleDeployment resource filter: %w", err)
	}
	if err := cfg.Cluster.ResourceFilter.Compile(); err != nil {
		return fmt.Errorf("cluster resource filter: %w", err)
	}
	if err := cfg.GitRepo.ResourceFilter.Compile(); err != nil {
		return fmt.Errorf("gitRepo resource filter: %w", err)
	}
	if err := cfg.HelmOp.ResourceFilter.Compile(); err != nil {
		return fmt.Errorf("helmOp resource filter: %w", err)
	}
	return nil
}

// logResourceFilters logs resource filter configuration for debugging
func logResourceFilters(cfg *ControllerLoggingConfig) {
	logFilter := func(controller string, filter *reconciler.ResourceFilter) {
		if filter != nil && (filter.NamespacePattern != "" || filter.NamePattern != "") {
			setupLog.Info("resource filter configured",
				"controller", controller,
				"namespacePattern", filter.NamespacePattern,
				"namePattern", filter.NamePattern,
			)
		}
	}

	logFilter("Bundle", cfg.Bundle.ResourceFilter)
	logFilter("BundleDeployment", cfg.BundleDeployment.ResourceFilter)
	logFilter("Cluster", cfg.Cluster.ResourceFilter)
	logFilter("GitRepo", cfg.GitRepo.ResourceFilter)
	logFilter("HelmOp", cfg.HelmOp.ResourceFilter)
}

// startSummaryPrinter periodically prints statistics summary
func startSummaryPrinter(ctx context.Context, interval time.Duration, reset bool) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	statsTracker := reconciler.GetStatsTracker()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			summary := statsTracker.GetSummary()

			// Convert to JSON and print
			jsonStr, err := summary.ToJSON()
			if err != nil {
				setupLog.Error(err, "failed to marshal summary to JSON")
				continue
			}

			// Print as structured log (will be formatted as JSON by zap)
			setupLog.Info("Fleet Monitor Summary", "summary", json.RawMessage(jsonStr))

			// Reset or just update timestamp
			if reset {
				statsTracker.Reset()
			} else {
				statsTracker.UpdateLastSummaryTime()
			}
		}
	}
}

// addBundleDownstreamResourceIndexer indexes Bundles by their DownstreamResources (secrets and configmaps).
// Required for the bundle monitor's Secret/ConfigMap watches.
func addBundleDownstreamResourceIndexer(ctx context.Context, mgr manager.Manager) error {
	return mgr.GetFieldIndexer().IndexField(
		ctx,
		&v1alpha1.Bundle{},
		config.BundleDownstreamResourceIndex,
		func(obj client.Object) []string {
			bundle, ok := obj.(*v1alpha1.Bundle)
			if !ok {
				return nil
			}

			var resources []string
			for _, dr := range bundle.Spec.DownstreamResources {
				lowerKind := strings.ToLower(dr.Kind)
				if lowerKind == "secret" || lowerKind == "configmap" {
					resources = append(resources, fmt.Sprintf("%s/%s", lowerKind, dr.Name))
				}
			}
			return resources
		},
	)
}

// addGitRepoSecretIndexers adds field indexers for GitRepo secret fields.
// Required for the gitrepo monitor's Secret watch.
func addGitRepoSecretIndexers(ctx context.Context, mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&v1alpha1.GitRepo{},
		config.GitRepoClientSecretNameIndex,
		func(obj client.Object) []string {
			gitRepo, ok := obj.(*v1alpha1.GitRepo)
			if !ok || gitRepo.Spec.ClientSecretName == "" {
				return nil
			}
			return []string{gitRepo.Spec.ClientSecretName}
		},
	); err != nil {
		return fmt.Errorf("GitRepoClientSecretName indexer: %w", err)
	}

	if err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&v1alpha1.GitRepo{},
		config.GitRepoHelmSecretNameIndex,
		func(obj client.Object) []string {
			gitRepo, ok := obj.(*v1alpha1.GitRepo)
			if !ok || gitRepo.Spec.HelmSecretName == "" {
				return nil
			}
			return []string{gitRepo.Spec.HelmSecretName}
		},
	); err != nil {
		return fmt.Errorf("GitRepoHelmSecretName indexer: %w", err)
	}

	if err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&v1alpha1.GitRepo{},
		config.GitRepoHelmSecretNameForPathsIndex,
		func(obj client.Object) []string {
			gitRepo, ok := obj.(*v1alpha1.GitRepo)
			if !ok || gitRepo.Spec.HelmSecretNameForPaths == "" {
				return nil
			}
			return []string{gitRepo.Spec.HelmSecretNameForPaths}
		},
	); err != nil {
		return fmt.Errorf("GitRepoHelmSecretNameForPaths indexer: %w", err)
	}

	return nil
}
