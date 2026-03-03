// Package monitor starts the fleet monitor.
package monitor

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	ctrl "sigs.k8s.io/controller-runtime"
	clog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/monitor/reconciler"
	"github.com/rancher/fleet/pkg/version"
)

type FleetMonitor struct {
	command.DebugConfig
	Kubeconfig string `usage:"Kubeconfig file"`
	Namespace  string `usage:"namespace to watch" default:"cattle-fleet-system" env:"NAMESPACE"`
	ShardID    string `usage:"only monitor resources labeled with a specific shard ID" name:"shard-id"`

	// Controller toggles (env vars parsed manually in Run — see parseBoolEnv)
	EnableBundleMonitor           bool `usage:"Enable bundle monitoring"`
	EnableBundleDeploymentMonitor bool `usage:"Enable bundledeployment monitoring"`
	EnableClusterMonitor          bool `usage:"Enable cluster monitoring"`
	EnableGitRepoMonitor          bool `usage:"Enable gitrepo monitoring"`
	EnableHelmOpMonitor           bool `usage:"Enable helmop monitoring"`

	// Per-controller logging modes (env vars parsed manually in Run — see parseBoolEnv)
	BundleDetailedLogs           bool `usage:"Enable detailed logging for Bundle controller"`
	BundleDeploymentDetailedLogs bool `usage:"Enable detailed logging for BundleDeployment controller"`
	ClusterDetailedLogs          bool `usage:"Enable detailed logging for Cluster controller"`
	GitRepoDetailedLogs          bool `usage:"Enable detailed logging for GitRepo controller"`
	HelmOpDetailedLogs           bool `usage:"Enable detailed logging for HelmOp controller"`

	// Bundle event filters (env vars parsed manually in Run — see parseBoolEnv)
	BundleEventFilterGenerationChange      bool `usage:"Show generation-change events for Bundle"`
	BundleEventFilterStatusChange          bool `usage:"Show status-change events for Bundle"`
	BundleEventFilterAnnotationChange      bool `usage:"Show annotation-change events for Bundle"`
	BundleEventFilterLabelChange           bool `usage:"Show label-change events for Bundle"`
	BundleEventFilterResourceVersionChange bool `usage:"Show resourceversion-change events for Bundle"`
	BundleEventFilterDeletion              bool `usage:"Show deletion events for Bundle"`
	BundleEventFilterNotFound              bool `usage:"Show not-found events for Bundle"`
	BundleEventFilterCreate                bool `usage:"Show create events for Bundle"`
	BundleEventFilterTriggeredBy           bool `usage:"Show triggered-by events for Bundle"`

	// BundleDeployment event filters (env vars parsed manually in Run — see parseBoolEnv)
	BundleDeploymentEventFilterGenerationChange      bool `usage:"Show generation-change events for BundleDeployment"`
	BundleDeploymentEventFilterStatusChange          bool `usage:"Show status-change events for BundleDeployment"`
	BundleDeploymentEventFilterAnnotationChange      bool `usage:"Show annotation-change events for BundleDeployment"`
	BundleDeploymentEventFilterLabelChange           bool `usage:"Show label-change events for BundleDeployment"`
	BundleDeploymentEventFilterResourceVersionChange bool `usage:"Show resourceversion-change events for BundleDeployment"`
	BundleDeploymentEventFilterDeletion              bool `usage:"Show deletion events for BundleDeployment"`
	BundleDeploymentEventFilterNotFound              bool `usage:"Show not-found events for BundleDeployment"`
	BundleDeploymentEventFilterCreate                bool `usage:"Show create events for BundleDeployment"`
	BundleDeploymentEventFilterTriggeredBy           bool `usage:"Show triggered-by events for BundleDeployment"`

	// Cluster event filters (env vars parsed manually in Run — see parseBoolEnv)
	ClusterEventFilterGenerationChange      bool `usage:"Show generation-change events for Cluster"`
	ClusterEventFilterStatusChange          bool `usage:"Show status-change events for Cluster"`
	ClusterEventFilterAnnotationChange      bool `usage:"Show annotation-change events for Cluster"`
	ClusterEventFilterLabelChange           bool `usage:"Show label-change events for Cluster"`
	ClusterEventFilterResourceVersionChange bool `usage:"Show resourceversion-change events for Cluster"`
	ClusterEventFilterDeletion              bool `usage:"Show deletion events for Cluster"`
	ClusterEventFilterNotFound              bool `usage:"Show not-found events for Cluster"`
	ClusterEventFilterCreate                bool `usage:"Show create events for Cluster"`
	ClusterEventFilterTriggeredBy           bool `usage:"Show triggered-by events for Cluster"`

	// GitRepo event filters (env vars parsed manually in Run — see parseBoolEnv)
	GitRepoEventFilterGenerationChange      bool `usage:"Show generation-change events for GitRepo"`
	GitRepoEventFilterStatusChange          bool `usage:"Show status-change events for GitRepo"`
	GitRepoEventFilterAnnotationChange      bool `usage:"Show annotation-change events for GitRepo"`
	GitRepoEventFilterLabelChange           bool `usage:"Show label-change events for GitRepo"`
	GitRepoEventFilterResourceVersionChange bool `usage:"Show resourceversion-change events for GitRepo"`
	GitRepoEventFilterDeletion              bool `usage:"Show deletion events for GitRepo"`
	GitRepoEventFilterNotFound              bool `usage:"Show not-found events for GitRepo"`
	GitRepoEventFilterCreate                bool `usage:"Show create events for GitRepo"`
	GitRepoEventFilterTriggeredBy           bool `usage:"Show triggered-by events for GitRepo"`

	// HelmOp event filters (env vars parsed manually in Run — see parseBoolEnv)
	HelmOpEventFilterGenerationChange      bool `usage:"Show generation-change events for HelmOp"`
	HelmOpEventFilterStatusChange          bool `usage:"Show status-change events for HelmOp"`
	HelmOpEventFilterAnnotationChange      bool `usage:"Show annotation-change events for HelmOp"`
	HelmOpEventFilterLabelChange           bool `usage:"Show label-change events for HelmOp"`
	HelmOpEventFilterResourceVersionChange bool `usage:"Show resourceversion-change events for HelmOp"`
	HelmOpEventFilterDeletion              bool `usage:"Show deletion events for HelmOp"`
	HelmOpEventFilterNotFound              bool `usage:"Show not-found events for HelmOp"`
	HelmOpEventFilterCreate                bool `usage:"Show create events for HelmOp"`
	HelmOpEventFilterTriggeredBy           bool `usage:"Show triggered-by events for HelmOp"`

	SummaryInterval string `usage:"How often to print summary (e.g., 5s, 30s, 1m)" env:"FLEET_EVENT_MONITOR_SUMMARY_INTERVAL" default:"30s"`
	SummaryReset    bool   `usage:"Reset counters after each summary"`
}

type MonitorReconcilerWorkers struct {
	Bundle           int
	BundleDeployment int
	Cluster          int
	GitRepo          int
	HelmOp           int
}

var (
	setupLog = ctrl.Log.WithName("setup")
	zopts    = &zap.Options{
		Development: true,
	}
)

func (f *FleetMonitor) PersistentPre(_ *cobra.Command, _ []string) error {
	if err := f.SetupDebug(); err != nil {
		return fmt.Errorf("failed to setup debug logging: %w", err)
	}
	zopts = f.OverrideZapOpts(zopts)

	return nil
}

func (f *FleetMonitor) Run(cmd *cobra.Command, args []string) error {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(zopts)))
	ctx := clog.IntoContext(cmd.Context(), ctrl.Log)

	kubeconfig := ctrl.GetConfigOrDie()
	workersOpts := MonitorReconcilerWorkers{}

	leaderOpts, err := command.NewLeaderElectionOptions()
	if err != nil {
		return err
	}

	if d := os.Getenv("BUNDLE_RECONCILER_WORKERS"); d != "" {
		w, err := strconv.Atoi(d)
		if err != nil {
			setupLog.Error(err, "failed to parse BUNDLE_RECONCILER_WORKERS", "value", d)
		}
		workersOpts.Bundle = w
	}

	if d := os.Getenv("BUNDLEDEPLOYMENT_RECONCILER_WORKERS"); d != "" {
		w, err := strconv.Atoi(d)
		if err != nil {
			setupLog.Error(err, "failed to parse BUNDLEDEPLOYMENT_RECONCILER_WORKERS", "value", d)
		}
		workersOpts.BundleDeployment = w
	}

	if d := os.Getenv("CLUSTER_RECONCILER_WORKERS"); d != "" {
		w, err := strconv.Atoi(d)
		if err != nil {
			setupLog.Error(err, "failed to parse CLUSTER_RECONCILER_WORKERS", "value", d)
		}
		workersOpts.Cluster = w
	}

	if d := os.Getenv("GITREPO_RECONCILER_WORKERS"); d != "" {
		w, err := strconv.Atoi(d)
		if err != nil {
			setupLog.Error(err, "failed to parse GITREPO_RECONCILER_WORKERS", "value", d)
		}
		workersOpts.GitRepo = w
	}

	if d := os.Getenv("HELMOP_RECONCILER_WORKERS"); d != "" {
		w, err := strconv.Atoi(d)
		if err != nil {
			setupLog.Error(err, "failed to parse HELMOP_RECONCILER_WORKERS", "value", d)
		}
		workersOpts.HelmOp = w
	}

	// The wrangler command framework does not reliably parse boolean env vars,
	// so all boolean env vars are parsed manually here. The struct fields
	// above intentionally omit env: tags to avoid a dual source of truth.
	parseBoolEnv := func(key string, defaultValue bool) bool {
		if val := os.Getenv(key); val != "" {
			b, err := strconv.ParseBool(val)
			if err != nil {
				setupLog.Error(err, "failed to parse boolean env var", "key", key, "value", val)
				return defaultValue
			}
			return b
		}
		return defaultValue
	}

	// Parse controller enable flags
	enableBundle := parseBoolEnv("ENABLE_BUNDLE_EVENT_MONITOR", f.EnableBundleMonitor)
	enableBundleDeployment := parseBoolEnv("ENABLE_BUNDLEDEPLOYMENT_EVENT_MONITOR", f.EnableBundleDeploymentMonitor)
	enableCluster := parseBoolEnv("ENABLE_CLUSTER_EVENT_MONITOR", f.EnableClusterMonitor)
	enableGitRepo := parseBoolEnv("ENABLE_GITREPO_EVENT_MONITOR", f.EnableGitRepoMonitor)
	enableHelmOp := parseBoolEnv("ENABLE_HELMOP_EVENT_MONITOR", f.EnableHelmOpMonitor)

	bundleDetailed := parseBoolEnv("FLEET_EVENT_MONITOR_BUNDLE_DETAILED", f.BundleDetailedLogs)
	bundleDeploymentDetailed := parseBoolEnv("FLEET_EVENT_MONITOR_BUNDLEDEPLOYMENT_DETAILED", f.BundleDeploymentDetailedLogs)
	clusterDetailed := parseBoolEnv("FLEET_EVENT_MONITOR_CLUSTER_DETAILED", f.ClusterDetailedLogs)
	gitRepoDetailed := parseBoolEnv("FLEET_EVENT_MONITOR_GITREPO_DETAILED", f.GitRepoDetailedLogs)
	helmOpDetailed := parseBoolEnv("FLEET_EVENT_MONITOR_HELMOP_DETAILED", f.HelmOpDetailedLogs)

	// Parse event filters for each controller
	bundleEventFilters := reconciler.EventTypeFilters{
		GenerationChange:      parseBoolEnv("FLEET_EVENT_MONITOR_BUNDLE_EVENT_GENERATION_CHANGE", f.BundleEventFilterGenerationChange),
		StatusChange:          parseBoolEnv("FLEET_EVENT_MONITOR_BUNDLE_EVENT_STATUS_CHANGE", f.BundleEventFilterStatusChange),
		AnnotationChange:      parseBoolEnv("FLEET_EVENT_MONITOR_BUNDLE_EVENT_ANNOTATION_CHANGE", f.BundleEventFilterAnnotationChange),
		LabelChange:           parseBoolEnv("FLEET_EVENT_MONITOR_BUNDLE_EVENT_LABEL_CHANGE", f.BundleEventFilterLabelChange),
		ResourceVersionChange: parseBoolEnv("FLEET_EVENT_MONITOR_BUNDLE_EVENT_RESVER_CHANGE", f.BundleEventFilterResourceVersionChange),
		Deletion:              parseBoolEnv("FLEET_EVENT_MONITOR_BUNDLE_EVENT_DELETION", f.BundleEventFilterDeletion),
		NotFound:              parseBoolEnv("FLEET_EVENT_MONITOR_BUNDLE_EVENT_NOT_FOUND", f.BundleEventFilterNotFound),
		Create:                parseBoolEnv("FLEET_EVENT_MONITOR_BUNDLE_EVENT_CREATE", f.BundleEventFilterCreate),
		TriggeredBy:           parseBoolEnv("FLEET_EVENT_MONITOR_BUNDLE_EVENT_TRIGGERED_BY", f.BundleEventFilterTriggeredBy),
	}

	bundleDeploymentEventFilters := reconciler.EventTypeFilters{
		GenerationChange:      parseBoolEnv("FLEET_EVENT_MONITOR_BD_EVENT_GENERATION_CHANGE", f.BundleDeploymentEventFilterGenerationChange),
		StatusChange:          parseBoolEnv("FLEET_EVENT_MONITOR_BD_EVENT_STATUS_CHANGE", f.BundleDeploymentEventFilterStatusChange),
		AnnotationChange:      parseBoolEnv("FLEET_EVENT_MONITOR_BD_EVENT_ANNOTATION_CHANGE", f.BundleDeploymentEventFilterAnnotationChange),
		LabelChange:           parseBoolEnv("FLEET_EVENT_MONITOR_BD_EVENT_LABEL_CHANGE", f.BundleDeploymentEventFilterLabelChange),
		ResourceVersionChange: parseBoolEnv("FLEET_EVENT_MONITOR_BD_EVENT_RESVER_CHANGE", f.BundleDeploymentEventFilterResourceVersionChange),
		Deletion:              parseBoolEnv("FLEET_EVENT_MONITOR_BD_EVENT_DELETION", f.BundleDeploymentEventFilterDeletion),
		NotFound:              parseBoolEnv("FLEET_EVENT_MONITOR_BD_EVENT_NOT_FOUND", f.BundleDeploymentEventFilterNotFound),
		Create:                parseBoolEnv("FLEET_EVENT_MONITOR_BD_EVENT_CREATE", f.BundleDeploymentEventFilterCreate),
		TriggeredBy:           parseBoolEnv("FLEET_EVENT_MONITOR_BD_EVENT_TRIGGERED_BY", f.BundleDeploymentEventFilterTriggeredBy),
	}

	clusterEventFilters := reconciler.EventTypeFilters{
		GenerationChange:      parseBoolEnv("FLEET_EVENT_MONITOR_CLUSTER_EVENT_GENERATION_CHANGE", f.ClusterEventFilterGenerationChange),
		StatusChange:          parseBoolEnv("FLEET_EVENT_MONITOR_CLUSTER_EVENT_STATUS_CHANGE", f.ClusterEventFilterStatusChange),
		AnnotationChange:      parseBoolEnv("FLEET_EVENT_MONITOR_CLUSTER_EVENT_ANNOTATION_CHANGE", f.ClusterEventFilterAnnotationChange),
		LabelChange:           parseBoolEnv("FLEET_EVENT_MONITOR_CLUSTER_EVENT_LABEL_CHANGE", f.ClusterEventFilterLabelChange),
		ResourceVersionChange: parseBoolEnv("FLEET_EVENT_MONITOR_CLUSTER_EVENT_RESVER_CHANGE", f.ClusterEventFilterResourceVersionChange),
		Deletion:              parseBoolEnv("FLEET_EVENT_MONITOR_CLUSTER_EVENT_DELETION", f.ClusterEventFilterDeletion),
		NotFound:              parseBoolEnv("FLEET_EVENT_MONITOR_CLUSTER_EVENT_NOT_FOUND", f.ClusterEventFilterNotFound),
		Create:                parseBoolEnv("FLEET_EVENT_MONITOR_CLUSTER_EVENT_CREATE", f.ClusterEventFilterCreate),
		TriggeredBy:           parseBoolEnv("FLEET_EVENT_MONITOR_CLUSTER_EVENT_TRIGGERED_BY", f.ClusterEventFilterTriggeredBy),
	}

	gitRepoEventFilters := reconciler.EventTypeFilters{
		GenerationChange:      parseBoolEnv("FLEET_EVENT_MONITOR_GITREPO_EVENT_GENERATION_CHANGE", f.GitRepoEventFilterGenerationChange),
		StatusChange:          parseBoolEnv("FLEET_EVENT_MONITOR_GITREPO_EVENT_STATUS_CHANGE", f.GitRepoEventFilterStatusChange),
		AnnotationChange:      parseBoolEnv("FLEET_EVENT_MONITOR_GITREPO_EVENT_ANNOTATION_CHANGE", f.GitRepoEventFilterAnnotationChange),
		LabelChange:           parseBoolEnv("FLEET_EVENT_MONITOR_GITREPO_EVENT_LABEL_CHANGE", f.GitRepoEventFilterLabelChange),
		ResourceVersionChange: parseBoolEnv("FLEET_EVENT_MONITOR_GITREPO_EVENT_RESVER_CHANGE", f.GitRepoEventFilterResourceVersionChange),
		Deletion:              parseBoolEnv("FLEET_EVENT_MONITOR_GITREPO_EVENT_DELETION", f.GitRepoEventFilterDeletion),
		NotFound:              parseBoolEnv("FLEET_EVENT_MONITOR_GITREPO_EVENT_NOT_FOUND", f.GitRepoEventFilterNotFound),
		Create:                parseBoolEnv("FLEET_EVENT_MONITOR_GITREPO_EVENT_CREATE", f.GitRepoEventFilterCreate),
		TriggeredBy:           parseBoolEnv("FLEET_EVENT_MONITOR_GITREPO_EVENT_TRIGGERED_BY", f.GitRepoEventFilterTriggeredBy),
	}

	helmOpEventFilters := reconciler.EventTypeFilters{
		GenerationChange:      parseBoolEnv("FLEET_EVENT_MONITOR_HELMOP_EVENT_GENERATION_CHANGE", f.HelmOpEventFilterGenerationChange),
		StatusChange:          parseBoolEnv("FLEET_EVENT_MONITOR_HELMOP_EVENT_STATUS_CHANGE", f.HelmOpEventFilterStatusChange),
		AnnotationChange:      parseBoolEnv("FLEET_EVENT_MONITOR_HELMOP_EVENT_ANNOTATION_CHANGE", f.HelmOpEventFilterAnnotationChange),
		LabelChange:           parseBoolEnv("FLEET_EVENT_MONITOR_HELMOP_EVENT_LABEL_CHANGE", f.HelmOpEventFilterLabelChange),
		ResourceVersionChange: parseBoolEnv("FLEET_EVENT_MONITOR_HELMOP_EVENT_RESVER_CHANGE", f.HelmOpEventFilterResourceVersionChange),
		Deletion:              parseBoolEnv("FLEET_EVENT_MONITOR_HELMOP_EVENT_DELETION", f.HelmOpEventFilterDeletion),
		NotFound:              parseBoolEnv("FLEET_EVENT_MONITOR_HELMOP_EVENT_NOT_FOUND", f.HelmOpEventFilterNotFound),
		Create:                parseBoolEnv("FLEET_EVENT_MONITOR_HELMOP_EVENT_CREATE", f.HelmOpEventFilterCreate),
		TriggeredBy:           parseBoolEnv("FLEET_EVENT_MONITOR_HELMOP_EVENT_TRIGGERED_BY", f.HelmOpEventFilterTriggeredBy),
	}

	// Parse resource filters for each controller
	bundleResourceFilter := &reconciler.ResourceFilter{
		NamespacePattern: os.Getenv("FLEET_EVENT_MONITOR_BUNDLE_RESOURCE_FILTER_NAMESPACE"),
		NamePattern:      os.Getenv("FLEET_EVENT_MONITOR_BUNDLE_RESOURCE_FILTER_NAME"),
	}

	bundleDeploymentResourceFilter := &reconciler.ResourceFilter{
		NamespacePattern: os.Getenv("FLEET_EVENT_MONITOR_BUNDLEDEPLOYMENT_RESOURCE_FILTER_NAMESPACE"),
		NamePattern:      os.Getenv("FLEET_EVENT_MONITOR_BUNDLEDEPLOYMENT_RESOURCE_FILTER_NAME"),
	}

	clusterResourceFilter := &reconciler.ResourceFilter{
		NamespacePattern: os.Getenv("FLEET_EVENT_MONITOR_CLUSTER_RESOURCE_FILTER_NAMESPACE"),
		NamePattern:      os.Getenv("FLEET_EVENT_MONITOR_CLUSTER_RESOURCE_FILTER_NAME"),
	}

	gitRepoResourceFilter := &reconciler.ResourceFilter{
		NamespacePattern: os.Getenv("FLEET_EVENT_MONITOR_GITREPO_RESOURCE_FILTER_NAMESPACE"),
		NamePattern:      os.Getenv("FLEET_EVENT_MONITOR_GITREPO_RESOURCE_FILTER_NAME"),
	}

	helmOpResourceFilter := &reconciler.ResourceFilter{
		NamespacePattern: os.Getenv("FLEET_EVENT_MONITOR_HELMOP_RESOURCE_FILTER_NAMESPACE"),
		NamePattern:      os.Getenv("FLEET_EVENT_MONITOR_HELMOP_RESOURCE_FILTER_NAME"),
	}

	// Log the parsed configuration for debugging
	setupLog.Info("parsed per-controller logging configuration",
		"bundle", bundleDetailed,
		"bundleDeployment", bundleDeploymentDetailed,
		"cluster", clusterDetailed,
		"gitRepo", gitRepoDetailed,
		"helmOp", helmOpDetailed,
	)

	// Parse summary interval
	summaryInterval, err := time.ParseDuration(f.SummaryInterval)
	if err != nil {
		setupLog.Error(err, "invalid summary interval, using default 30s", "value", f.SummaryInterval)
		summaryInterval = 30 * time.Second
	}

	monitorOpts := MonitorOptions{
		EnableBundle:           enableBundle,
		EnableBundleDeployment: enableBundleDeployment,
		EnableCluster:          enableCluster,
		EnableGitRepo:          enableGitRepo,
		EnableHelmOp:           enableHelmOp,
		Workers:                workersOpts,

		// Per-controller logging configuration
		ControllerLogging: ControllerLoggingConfig{
			Bundle: ControllerLogConfig{
				Detailed:       bundleDetailed,
				EventFilters:   bundleEventFilters,
				ResourceFilter: bundleResourceFilter,
			},
			BundleDeployment: ControllerLogConfig{
				Detailed:       bundleDeploymentDetailed,
				EventFilters:   bundleDeploymentEventFilters,
				ResourceFilter: bundleDeploymentResourceFilter,
			},
			Cluster: ControllerLogConfig{
				Detailed:       clusterDetailed,
				EventFilters:   clusterEventFilters,
				ResourceFilter: clusterResourceFilter,
			},
			GitRepo: ControllerLogConfig{
				Detailed:       gitRepoDetailed,
				EventFilters:   gitRepoEventFilters,
				ResourceFilter: gitRepoResourceFilter,
			},
			HelmOp: ControllerLogConfig{
				Detailed:       helmOpDetailed,
				EventFilters:   helmOpEventFilters,
				ResourceFilter: helmOpResourceFilter,
			},
		},

		SummaryInterval: summaryInterval,
		SummaryReset:    parseBoolEnv("FLEET_EVENT_MONITOR_SUMMARY_RESET", f.SummaryReset),
	}

	if err := start(
		ctx,
		f.Namespace,
		kubeconfig,
		leaderOpts,
		monitorOpts,
		f.ShardID,
	); err != nil {
		return err
	}

	<-cmd.Context().Done()
	return nil
}

func App() *cobra.Command {
	root := command.Command(&FleetMonitor{}, cobra.Command{
		Version: version.FriendlyVersion(),
		Use:     "fleeteventmonitor",
		Short:   "Fleet read-only monitoring controllers",
	})
	fs := flag.NewFlagSet("", flag.ExitOnError)
	zopts.BindFlags(fs)
	ctrl.RegisterFlags(fs)
	root.Flags().AddGoFlagSet(fs)

	return root
}
