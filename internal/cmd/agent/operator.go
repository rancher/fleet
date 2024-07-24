package agent

import (
	"context"
	"flag"
	"os"

	"github.com/rancher/fleet/internal/cmd/agent/controller"
	"github.com/rancher/fleet/internal/cmd/agent/deployer"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/applied"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/cleanup"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/driftdetect"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/monitor"
	"github.com/rancher/fleet/internal/cmd/agent/register"
	"github.com/rancher/fleet/internal/cmd/agent/trigger"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/helmdeployer"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"helm.sh/helm/v3/pkg/cli"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	scheme      = runtime.NewScheme()
	localScheme = runtime.NewScheme()
)

// defaultNamespace is the namespace to use for resources that don't specify a namespace, e.g. "default"
const defaultNamespace = "default"

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme

	utilruntime.Must(clientgoscheme.AddToScheme(localScheme))
}

// start the fleet agent
// systemNamespace is the namespace the agent is running in, e.g. cattle-fleet-system
func start(ctx context.Context, localConfig *rest.Config, systemNamespace, agentScope string) error {
	// Registration is done in an init container. If we are here, we are already registered.
	// Retrieve the existing config from the registration.
	// Cannot start without kubeconfig for upstream cluster:
	upstreamConfig, agentConfig, fleetNamespace, clusterName, err := loadRegistration(ctx, systemNamespace, localConfig)
	if err != nil {
		setupLog.Error(err, "unable to load registration and start manager")
		return err
	}

	// Start manager for upstream cluster, we do not use leader election
	setupLog.Info("listening for changes on upstream cluster", "cluster", clusterName, "namespace", fleetNamespace)

	metricsAddr := ":8080"
	probeAddr := ":8081"

	mgr, err := ctrl.NewManager(upstreamConfig, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         false,
		// only watch resources in the fleet namespace
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{fleetNamespace: {}},
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		return err
	}

	localCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	reconciler, err := newReconciler(
		ctx,
		localCtx,
		mgr,
		localConfig,
		systemNamespace,
		fleetNamespace,
		agentScope,
		agentConfig,
	)
	if err != nil {
		setupLog.Error(err, "unable to set up bundledeployment reconciler")
		return err
	}

	// Set up the bundledeployment reconciler
	if err = (reconciler).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "BundleDeployment")
		return err
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		return err
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		return err
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		return err

	}

	return nil
}

func loadRegistration(
	ctx context.Context,
	systemNamespace string,
	localConfig *rest.Config,
) (*rest.Config, config.Config, string, string, error) {
	agentInfo, agentConfig, err := register.Get(ctx, systemNamespace, localConfig)
	if err != nil {
		setupLog.Error(err, "cannot get registration info for upstream cluster")
		return nil, config.Config{}, "", "", err
	}

	// fleetNamespace is the upstream cluster namespace from AgentInfo, e.g. cluster-fleet-ID
	fleetNamespace, _, err := agentInfo.ClientConfig.Namespace()
	if err != nil {
		setupLog.Error(err, "cannot get upstream namespace from registration")
		return nil, config.Config{}, "", "", err
	}

	upstreamConfig, err := agentInfo.ClientConfig.ClientConfig()
	if err != nil {
		setupLog.Error(err, "cannot get upstream kubeconfig from registration")
		return nil, config.Config{}, "", "", err
	}

	if agentConfig == nil {
		setupLog.Error(err, "unexpected nil agent config")
		return nil, config.Config{}, "", "", err
	}

	return upstreamConfig, *agentConfig, fleetNamespace, agentInfo.ClusterName, nil
}

func newReconciler(
	ctx context.Context,
	localCtx context.Context,
	mgr manager.Manager,
	localConfig *rest.Config,
	systemNamespace string,
	fleetNamespace string,
	agentScope string,
	agentConfig config.Config,
) (*controller.BundleDeploymentReconciler, error) {
	upstreamClient := mgr.GetClient()

	// Build client for local cluster
	localCluster, err := newCluster(localCtx, localConfig, ctrl.Options{
		Scheme: localScheme,
		Logger: mgr.GetLogger().WithName("local-cluster"),
	})
	if err != nil {
		setupLog.Error(err, "unable to build local cluster client")
		return nil, err
	}
	localClient := localCluster.GetClient()

	if kubeconfig := flag.Lookup("kubeconfig").Value.String(); kubeconfig != "" {
		// set KUBECONFIG env var so helm can find it
		os.Setenv("KUBECONFIG", kubeconfig)
	}

	// Build the helm deployer, which uses a getter for local cluster's client-go client for helm SDK
	helmDeployer := helmdeployer.New(
		systemNamespace,
		defaultNamespace,
		defaultNamespace,
		agentScope,
	)
	err = helmDeployer.Setup(ctx, localClient, cli.New().RESTClientGetter())
	if err != nil {
		setupLog.Error(err, "unable to setup local helm SDK client")
		return nil, err
	}

	// Build the deployer that the bundledeployment reconciler will use
	deployer := deployer.New(
		localClient,
		mgr.GetAPIReader(),
		manifest.NewLookup(),
		helmDeployer,
	)

	// Build the monitor to update the bundle deployment's status, calculates modified/non-modified
	localDynamic, err := dynamic.NewForConfig(localConfig)
	if err != nil {
		return nil, err
	}
	applied, err := applied.NewWithClient(localConfig)
	if err != nil {
		return nil, err
	}
	monitor := monitor.New(
		localClient,
		applied,
		helmDeployer,
		defaultNamespace,
		agentScope,
	)

	// Build the drift detector for deployed resources
	trigger := trigger.New(ctx, localDynamic, localCluster.GetRESTMapper())
	driftdetect := driftdetect.New(
		trigger,
		upstreamClient,
		mgr.GetAPIReader(),
		applied,
		defaultNamespace,
		defaultNamespace,
		agentScope,
	)

	// Build the clean up, which deletes helm releases
	cleanup := cleanup.New(
		upstreamClient,
		localClient.RESTMapper(),
		localDynamic,
		helmDeployer,
		fleetNamespace,
		defaultNamespace,
		agentConfig.GarbageCollectionInterval.Duration,
	)

	return &controller.BundleDeploymentReconciler{
		Client: upstreamClient,

		Scheme:      mgr.GetScheme(),
		LocalClient: localClient,

		Deployer:    deployer,
		Monitor:     monitor,
		DriftDetect: driftdetect,
		Cleanup:     cleanup,

		DefaultNamespace: defaultNamespace,

		AgentScope: agentScope,
	}, nil
}

// newCluster returns a new cluster client, see controller-runtime/pkg/manager/manager.go
// This client is for the local cluster, not the upstream cluster. The upstream
// cluster client is used by the manager to watch for changes to the
// bundledeployments.
func newCluster(ctx context.Context, config *rest.Config, options manager.Options) (cluster.Cluster, error) {
	cluster, err := cluster.New(config, func(clusterOptions *cluster.Options) {
		clusterOptions.Scheme = options.Scheme
		clusterOptions.Logger = options.Logger
	})
	if err != nil {
		return nil, err
	}
	go func() {
		err := cluster.GetCache().Start(ctx)
		if err != nil {
			setupLog.Error(err, "unable to start the cache")
			os.Exit(1)
		}
	}()
	cluster.GetCache().WaitForCacheSync(ctx)

	return cluster, nil
}
