// Package agent provides the agent controller. (fleetagent)
package agent

import (
	"context"
	"sync"
	"time"

	"github.com/rancher/fleet/modules/agent/pkg/controllers"
	"github.com/rancher/fleet/modules/agent/pkg/register"

	"github.com/rancher/lasso/pkg/mapper"
	"github.com/rancher/wrangler/pkg/kubeconfig"
	"github.com/rancher/wrangler/pkg/ratelimit"
	"github.com/rancher/wrangler/pkg/ticker"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

type Options struct {
	DefaultNamespace string
	ClusterID        string
	NoLeaderElect    bool
	CheckinInterval  time.Duration
	StartAfter       <-chan struct{}
}

// Register is only used by simulators to start an agent
func Register(ctx context.Context, kubeConfig, namespace, clusterID string) error {
	clientConfig := kubeconfig.GetNonInteractiveClientConfig(kubeConfig)
	kc, err := clientConfig.ClientConfig()
	if err != nil {
		return err
	}
	kc.RateLimiter = ratelimit.None

	_, err = register.Register(ctx, namespace, clusterID, kc)
	return err
}

// Start the fleet agent
func Start(ctx context.Context, kubeConfig, namespace, agentScope string, opts *Options) error {
	if opts == nil {
		opts = &Options{}
	}
	if opts.DefaultNamespace == "" {
		opts.DefaultNamespace = "default"
	}

	clientConfig := kubeconfig.GetNonInteractiveClientConfig(kubeConfig)
	kc, err := clientConfig.ClientConfig()
	if err != nil {
		return err
	}

	agentInfo, err := register.Register(ctx, namespace, opts.ClusterID, kc)
	if err != nil {
		return err
	}

	fleetNamespace, _, err := agentInfo.ClientConfig.Namespace()
	if err != nil {
		return err
	}

	fleetRestConfig, err := agentInfo.ClientConfig.ClientConfig()
	if err != nil {
		return err
	}

	fleetMapper, mapper, discovery, err := NewMappers(ctx, fleetRestConfig, clientConfig, opts)
	if err != nil {
		return err
	}

	return controllers.Register(ctx,
		!opts.NoLeaderElect,
		fleetNamespace,
		namespace,
		opts.DefaultNamespace,
		agentScope,
		agentInfo.ClusterNamespace,
		agentInfo.ClusterName,
		opts.CheckinInterval,
		fleetRestConfig,
		clientConfig,
		fleetMapper,
		mapper,
		discovery,
		opts.StartAfter)
}

var (
	mapperLock        sync.Mutex
	cachedFleetMapper meta.RESTMapper
	cachedMapper      meta.RESTMapper
	cachedDiscovery   discovery.CachedDiscoveryInterface
)

// Share mappers across simulators
func NewMappers(ctx context.Context, fleetRESTConfig *rest.Config, clientconfig clientcmd.ClientConfig, opts *Options) (meta.RESTMapper, meta.RESTMapper, discovery.CachedDiscoveryInterface, error) {
	mapperLock.Lock()
	defer mapperLock.Unlock()

	if cachedFleetMapper != nil &&
		cachedMapper != nil &&
		cachedDiscovery != nil {
		return cachedFleetMapper, cachedMapper, cachedDiscovery, nil
	}

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

	cachedFleetMapper = fleetMapper
	cachedMapper = mapper
	cachedDiscovery = discovery

	return fleetMapper, mapper, discovery, nil
}
