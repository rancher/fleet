package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	memory "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	command "github.com/rancher/fleet/internal/cmd"
	"github.com/rancher/fleet/internal/cmd/agent/clusterstatus"
	"github.com/rancher/fleet/internal/cmd/agent/register"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"

	cache2 "github.com/rancher/lasso/pkg/cache"
	"github.com/rancher/lasso/pkg/client"
	"github.com/rancher/lasso/pkg/controller"
	"github.com/rancher/lasso/pkg/mapper"
	"github.com/rancher/wrangler/v3/pkg/generated/controllers/core"
	"github.com/rancher/wrangler/v3/pkg/kubeconfig"
	"github.com/rancher/wrangler/v3/pkg/ratelimit"
	"github.com/rancher/wrangler/v3/pkg/ticker"
)

func NewClusterStatus() *cobra.Command {
	cmd := command.Command(&ClusterStatus{}, cobra.Command{
		Use:   "clusterstatus [flags]",
		Short: "Continuously report resource status to the upstream cluster",
	})
	return cmd
}

type ClusterStatus struct {
	command.DebugConfig
	UpstreamOptions
	CheckinInterval string `usage:"How often to post cluster status" env:"CHECKIN_INTERVAL"`
}

// HelpFunc hides the global agent-scope flag from the help output
func (c *ClusterStatus) HelpFunc(cmd *cobra.Command, strings []string) {
	_ = cmd.Flags().MarkHidden("agent-scope")
	cmd.Parent().HelpFunc()(cmd, strings)
}

func (cs *ClusterStatus) PersistentPre(cmd *cobra.Command, _ []string) error {
	if err := cs.SetupDebug(); err != nil {
		return fmt.Errorf("failed to setup debug logging: %w", err)
	}
	zopts = cs.OverrideZapOpts(zopts)
	return nil
}

func (cs *ClusterStatus) Run(cmd *cobra.Command, args []string) error {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(zopts)))
	ctx := log.IntoContext(cmd.Context(), ctrl.Log)

	var err error
	var checkinInterval time.Duration
	if cs.CheckinInterval != "" {
		checkinInterval, err = time.ParseDuration(cs.CheckinInterval)
		if err != nil {
			return err
		}
	}

	clientConfig := kubeconfig.GetNonInteractiveClientConfig(cs.Kubeconfig)
	kc, err := clientConfig.ClientConfig()
	if err != nil {
		setupLog.Error(err, "failed to get kubeconfig")
		return err
	}

	// without rate limiting
	localConfig := rest.CopyConfig(kc)
	localConfig.RateLimiter = ratelimit.None

	// cannot start without kubeconfig for upstream cluster
	setupLog.Info("Fetching kubeconfig for upstream cluster from registration", "namespace", cs.Namespace)
	agentInfo, err := register.Get(ctx, cs.Namespace, localConfig)
	if err != nil {
		setupLog.Error(err, "failed to get kubeconfig from upstream cluster")
		return err
	}

	// set up factory for upstream cluster
	fleetNamespace, _, err := agentInfo.ClientConfig.Namespace()
	if err != nil {
		setupLog.Error(err, "failed to get namespace from upstream cluster")
		return err
	}

	fleetRESTConfig, err := agentInfo.ClientConfig.ClientConfig()
	if err != nil {
		setupLog.Error(err, "failed to get kubeconfig from upstream cluster")
		return err
	}

	//  now we have both configs
	fleetMapper, mapper, _, err := newMappers(ctx, fleetRESTConfig, clientConfig)
	if err != nil {
		setupLog.Error(err, "failed to get mappers")
		return err
	}

	fleetSharedFactory, err := newSharedControllerFactory(fleetRESTConfig, fleetMapper, fleetNamespace)
	if err != nil {
		setupLog.Error(err, "failed to build shared controller factory")
		return err
	}

	fleetFactory, err := fleet.NewFactoryFromConfigWithOptions(fleetRESTConfig, &fleet.FactoryOptions{
		SharedControllerFactory: fleetSharedFactory,
	})
	if err != nil {
		setupLog.Error(err, "failed to build fleet factory")
		return err
	}

	// set up factory for local cluster
	localFactory, err := newSharedControllerFactory(localConfig, mapper, "")
	if err != nil {
		setupLog.Error(err, "failed to build shared controller factory")
		return err
	}

	coreFactory, err := core.NewFactoryFromConfigWithOptions(localConfig, &core.FactoryOptions{
		SharedControllerFactory: localFactory,
	})
	if err != nil {
		setupLog.Error(err, "failed to build core factory")
		return err
	}

	setupLog.Info("Starting cluster status ticker", "checkin interval", checkinInterval.String(), "cluster namespace", agentInfo.ClusterNamespace, "cluster name", agentInfo.ClusterName)

	clusterstatus.Ticker(ctx,
		cs.Namespace,
		agentInfo.ClusterNamespace,
		agentInfo.ClusterName,
		checkinInterval,
		coreFactory.Core().V1().Node(),
		fleetFactory.Fleet().V1alpha1().Cluster(),
	)

	<-cmd.Context().Done()

	return nil
}

func newSharedControllerFactory(config *rest.Config, mapper meta.RESTMapper, namespace string) (controller.SharedControllerFactory, error) {
	cf, err := client.NewSharedClientFactory(config, &client.SharedClientFactoryOptions{
		Mapper: mapper,
	})
	if err != nil {
		return nil, err
	}

	cacheFactory := cache2.NewSharedCachedFactory(cf, &cache2.SharedCacheFactoryOptions{
		DefaultNamespace: namespace,
		DefaultResync:    durations.DefaultResyncAgent,
	})
	slowRateLimiter := workqueue.NewItemExponentialFailureRateLimiter(durations.SlowFailureRateLimiterBase, durations.SlowFailureRateLimiterMax)

	return controller.NewSharedControllerFactory(cacheFactory, &controller.SharedControllerFactoryOptions{
		KindRateLimiter: map[schema.GroupVersionKind]workqueue.RateLimiter{
			v1alpha1.SchemeGroupVersion.WithKind("BundleDeployment"): slowRateLimiter,
		},
	}), nil
}

func newMappers(ctx context.Context, fleetRESTConfig *rest.Config, clientconfig clientcmd.ClientConfig) (meta.RESTMapper, meta.RESTMapper, discovery.CachedDiscoveryInterface, error) {
	fleetMapper, err := mapper.New(fleetRESTConfig)
	if err != nil {
		return nil, nil, nil, err
	}

	client, err := clientconfig.ClientConfig()
	if err != nil {
		return nil, nil, nil, err
	}
	client.RateLimiter = ratelimit.None

	d, err := discovery.NewDiscoveryClientForConfig(client)
	if err != nil {
		return nil, nil, nil, err
	}
	discovery := memory.NewMemCacheClient(d)
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(discovery)

	go func() {
		for range ticker.Context(ctx, 30*time.Second) {
			discovery.Invalidate()
			mapper.Reset()
		}
	}()

	return fleetMapper, mapper, discovery, nil
}
