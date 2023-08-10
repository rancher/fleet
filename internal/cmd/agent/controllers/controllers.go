// Package controllers wires and starts the controllers for the agent.
package controllers

import (
	"context"
	"time"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/workqueue"

	"github.com/rancher/fleet/internal/cmd/agent/controllers/bundledeployment"
	"github.com/rancher/fleet/internal/cmd/agent/controllers/cluster"
	"github.com/rancher/fleet/internal/cmd/agent/deployer"
	"github.com/rancher/fleet/internal/cmd/agent/trigger"
	"github.com/rancher/fleet/internal/helmdeployer"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"

	cache2 "github.com/rancher/lasso/pkg/cache"
	"github.com/rancher/lasso/pkg/client"
	"github.com/rancher/lasso/pkg/controller"
	"github.com/rancher/wrangler/pkg/apply"
	batchcontrollers "github.com/rancher/wrangler/pkg/generated/controllers/batch/v1"
	"github.com/rancher/wrangler/pkg/generated/controllers/core"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/start"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type AppContext struct {
	Fleet    fleetcontrollers.Interface
	Core     corecontrollers.Interface
	Batch    batchcontrollers.Interface
	Dynamic  dynamic.Interface
	Apply    apply.Apply
	starters []start.Starter

	ClusterNamespace string
	ClusterName      string
	AgentNamespace   string

	clientConfig             clientcmd.ClientConfig
	restConfig               *rest.Config
	cachedDiscoveryInterface discovery.CachedDiscoveryInterface
	restMapper               meta.RESTMapper
}

func (a *AppContext) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return a.clientConfig
}

func (a *AppContext) ToRESTConfig() (*rest.Config, error) {
	return a.restConfig, nil
}

func (a *AppContext) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	return a.cachedDiscoveryInterface, nil
}

func (a *AppContext) ToRESTMapper() (meta.RESTMapper, error) {
	return a.restMapper, nil
}

func (a *AppContext) Start(ctx context.Context) error {
	return start.All(ctx, 5, a.starters...)
}

func Register(ctx context.Context,
	appCtx *AppContext,
	fleetNamespace, defaultNamespace, agentScope string,
	checkinInterval time.Duration) error {

	labelPrefix := "fleet"
	if defaultNamespace != "" {
		labelPrefix = defaultNamespace
	}

	helmDeployer, err := helmdeployer.NewHelm(appCtx.AgentNamespace, defaultNamespace, labelPrefix, agentScope, appCtx,
		appCtx.Core.ServiceAccount().Cache(), appCtx.Core.ConfigMap().Cache(), appCtx.Core.Secret().Cache())
	if err != nil {
		return err
	}

	bundledeployment.Register(ctx,
		trigger.New(ctx, appCtx.restMapper, appCtx.Dynamic),
		appCtx.restMapper,
		appCtx.Dynamic,
		deployer.NewManager(
			fleetNamespace,
			defaultNamespace,
			labelPrefix,
			agentScope,
			appCtx.Fleet.BundleDeployment().Cache(),
			appCtx.Fleet.BundleDeployment(),
			manifest.NewLookup(appCtx.Fleet.Content()),
			helmDeployer,
			appCtx.Apply),
		appCtx.Fleet.BundleDeployment())

	cluster.Register(ctx,
		appCtx.AgentNamespace,
		appCtx.ClusterNamespace,
		appCtx.ClusterName,
		checkinInterval,
		appCtx.Core.Node().Cache(),
		appCtx.Fleet.Cluster())

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

func NewAppContext(fleetNamespace, agentNamespace, clusterNamespace, clusterName string,
	fleetRESTConfig *rest.Config, clientConfig clientcmd.ClientConfig,
	fleetMapper, mapper meta.RESTMapper, discovery discovery.CachedDiscoveryInterface) (*AppContext, error) {

	// set up factory for upstream cluster
	fleetFactory, err := newSharedControllerFactory(fleetRESTConfig, fleetMapper, fleetNamespace)
	if err != nil {
		return nil, err
	}

	fleet, err := fleet.NewFactoryFromConfigWithOptions(fleetRESTConfig, &fleet.FactoryOptions{
		SharedControllerFactory: fleetFactory,
	})
	if err != nil {
		return nil, err
	}

	localConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	// set up factory for local cluster
	localFactory, err := newSharedControllerFactory(localConfig, mapper, "")
	if err != nil {
		return nil, err
	}

	core, err := core.NewFactoryFromConfigWithOptions(localConfig, &core.FactoryOptions{
		SharedControllerFactory: localFactory,
	})
	if err != nil {
		return nil, err
	}

	apply, err := apply.NewForConfig(localConfig)
	if err != nil {
		return nil, err
	}

	dynamic, err := dynamic.NewForConfig(localConfig)
	if err != nil {
		return nil, err
	}

	return &AppContext{
		Dynamic:                  dynamic,
		Apply:                    apply,
		Fleet:                    fleet.Fleet().V1alpha1(),
		Core:                     core.Core().V1(),
		ClusterNamespace:         clusterNamespace,
		ClusterName:              clusterName,
		AgentNamespace:           agentNamespace,
		clientConfig:             clientConfig,
		restConfig:               localConfig,
		cachedDiscoveryInterface: discovery,
		restMapper:               mapper,
		starters: []start.Starter{
			core,
			fleet,
		},
	}, nil
}
