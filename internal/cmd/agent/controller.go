package agent

import (
	"context"
	"time"

	"github.com/rancher/fleet/internal/cmd/agent/controllers"
	"github.com/rancher/fleet/internal/cmd/agent/register"

	"github.com/rancher/lasso/pkg/mapper"
	"github.com/rancher/wrangler/v2/pkg/kubeconfig"
	"github.com/rancher/wrangler/v2/pkg/ratelimit"
	"github.com/rancher/wrangler/v2/pkg/ticker"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

// defaultNamespace is the namespace to use for resources that don't specify a namespace, e.g. "default"
const defaultNamespace = "default"

// start the fleet agent
func start(ctx context.Context, kubeConfig, namespace, agentScope string) error {
	clientConfig := kubeconfig.GetNonInteractiveClientConfig(kubeConfig)
	kc, err := clientConfig.ClientConfig()
	if err != nil {
		return err
	}

	agentInfo, err := register.Get(ctx, namespace, kc)
	if err != nil {
		setupLog.Error(err, "failed to get registration for upstream cluster")
		return err
	}

	fleetNamespace, _, err := agentInfo.ClientConfig.Namespace()
	if err != nil {
		setupLog.Error(err, "failed to get namespace from upstream cluster")
		return err
	}

	fleetRESTConfig, err := agentInfo.ClientConfig.ClientConfig()
	if err != nil {
		setupLog.Error(err, "failed to get kubeconfig for upstream cluster")
		return err
	}

	fleetMapper, mapper, discovery, err := newMappers(ctx, fleetRESTConfig, clientConfig)
	if err != nil {
		setupLog.Error(err, "failed to get mappers")
		return err
	}

	appCtx, err := controllers.NewAppContext(
		fleetNamespace, namespace, agentInfo.ClusterNamespace, agentInfo.ClusterName,
		fleetRESTConfig, clientConfig, fleetMapper, mapper, discovery)
	if err != nil {
		setupLog.Error(err, "failed to create app context")
		return err
	}

	err = controllers.Register(ctx,
		appCtx,
		fleetNamespace, defaultNamespace,
		agentScope)
	if err != nil {
		setupLog.Error(err, "failed to register controllers")
		return err
	}

	if err := appCtx.Start(ctx); err != nil {
		setupLog.Error(err, "failed to start app context")
		return err
	}

	return nil
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
