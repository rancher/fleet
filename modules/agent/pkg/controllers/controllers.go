// Package controllers wires and starts the controllers for the agent. (fleetagent)
package controllers

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/modules/agent/pkg/controllers/bundledeployment"
	"github.com/rancher/fleet/modules/agent/pkg/controllers/cluster"
	"github.com/rancher/fleet/modules/agent/pkg/deployer"
	"github.com/rancher/fleet/modules/agent/pkg/trigger"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/helmdeployer"
	"github.com/rancher/fleet/pkg/manifest"

	cache2 "github.com/rancher/lasso/pkg/cache"
	"github.com/rancher/lasso/pkg/client"
	"github.com/rancher/lasso/pkg/controller"
	"github.com/rancher/wrangler/pkg/apply"
	batchcontrollers "github.com/rancher/wrangler/pkg/generated/controllers/batch/v1"
	"github.com/rancher/wrangler/pkg/generated/controllers/core"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/leader"
	"github.com/rancher/wrangler/pkg/ratelimit"
	"github.com/rancher/wrangler/pkg/start"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type appContext struct {
	Fleet    fleetcontrollers.Interface
	Core     corecontrollers.Interface
	Batch    batchcontrollers.Interface
	Dynamic  dynamic.Interface
	K8s      kubernetes.Interface
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

func (a *appContext) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return a.clientConfig
}

func (a *appContext) ToRESTConfig() (*rest.Config, error) {
	return a.restConfig, nil
}

func (a *appContext) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	return a.cachedDiscoveryInterface, nil
}

func (a *appContext) ToRESTMapper() (meta.RESTMapper, error) {
	return a.restMapper, nil
}

func (a *appContext) start(ctx context.Context) error {
	return start.All(ctx, 5, a.starters...)
}

func Register(ctx context.Context, leaderElect bool,
	fleetNamespace, agentNamespace, defaultNamespace, agentScope, clusterNamespace, clusterName string,
	checkinInterval time.Duration,
	fleetConfig *rest.Config, clientConfig clientcmd.ClientConfig,
	fleetMapper, mapper meta.RESTMapper,
	discovery discovery.CachedDiscoveryInterface,
	startChan <-chan struct{}) error {
	appCtx, err := newContext(fleetNamespace, agentNamespace, clusterNamespace, clusterName,
		fleetConfig, clientConfig, fleetMapper, mapper, discovery)
	if err != nil {
		return err
	}

	labelPrefix := "fleet"
	if defaultNamespace != "" {
		labelPrefix = defaultNamespace
	}

	helmDeployer, err := helmdeployer.NewHelm(agentNamespace, defaultNamespace, labelPrefix, agentScope, appCtx,
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

	if leaderElect {
		leader.RunOrDie(ctx, agentNamespace, "fleet-agent-lock", appCtx.K8s, func(ctx context.Context) {
			if err := appCtx.start(ctx); err != nil {
				logrus.Fatal(err)
			}
		})
	} else if startChan != nil {
		go func() {
			<-startChan
			logrus.Fatalf("failed to start: %v", appCtx.start(ctx))
		}()
	} else {
		return appCtx.start(ctx)
	}

	return nil
}

func newSharedControllerFactory(config *rest.Config, mapper meta.RESTMapper, namespace string) (controller.SharedControllerFactory, error) {
	cf, err := client.NewSharedClientFactory(config, &client.SharedClientFactoryOptions{
		Mapper: mapper,
	})
	if err != nil {
		return nil, err
	}
	return controller.NewSharedControllerFactory(cache2.NewSharedCachedFactory(cf, &cache2.SharedCacheFactoryOptions{
		DefaultNamespace: namespace,
		DefaultResync:    durations.DefaultResyncAgent,
	}), nil), nil
}

func newContext(fleetNamespace, agentNamespace, clusterNamespace, clusterName string,
	fleetConfig *rest.Config, clientConfig clientcmd.ClientConfig,
	fleetMapper, mapper meta.RESTMapper, discovery discovery.CachedDiscoveryInterface) (*appContext, error) {
	localConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	fleetFactory, err := newSharedControllerFactory(fleetConfig, fleetMapper, fleetNamespace)
	if err != nil {
		return nil, err
	}

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
	corev := core.Core().V1()

	fleet, err := fleet.NewFactoryFromConfigWithOptions(fleetConfig, &fleet.FactoryOptions{
		SharedControllerFactory: fleetFactory,
	})
	if err != nil {
		return nil, err
	}
	fleetv := fleet.Fleet().V1alpha1()

	apply, err := apply.NewForConfig(localConfig)
	if err != nil {
		return nil, err
	}

	dynamic, err := dynamic.NewForConfig(localConfig)
	if err != nil {
		return nil, err
	}

	localConfig = rest.CopyConfig(localConfig)
	localConfig.RateLimiter = ratelimit.None

	k8s, err := kubernetes.NewForConfig(localConfig)
	if err != nil {
		return nil, err
	}

	return &appContext{
		Dynamic:          dynamic,
		Apply:            apply,
		Fleet:            fleetv,
		Core:             corev,
		K8s:              k8s,
		ClusterNamespace: clusterNamespace,
		ClusterName:      clusterName,
		AgentNamespace:   agentNamespace,

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
