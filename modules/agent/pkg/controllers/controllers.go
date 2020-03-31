package controllers

import (
	"context"
	"time"

	"github.com/rancher/fleet/modules/agent/pkg/controllers/bundledeployment"
	"github.com/rancher/fleet/modules/agent/pkg/controllers/secret"
	"github.com/rancher/fleet/modules/agent/pkg/deployer"
	"github.com/rancher/fleet/modules/agent/pkg/trigger"
	"github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/helmdeployer"
	"github.com/rancher/fleet/pkg/manifest"
	batch2 "github.com/rancher/wrangler-api/pkg/generated/controllers/batch"
	batchcontrollers "github.com/rancher/wrangler-api/pkg/generated/controllers/batch/v1"
	"github.com/rancher/wrangler-api/pkg/generated/controllers/core"
	corecontrollers "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler-api/pkg/generated/controllers/rbac"
	rbaccontrollers "github.com/rancher/wrangler-api/pkg/generated/controllers/rbac/v1"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/leader"
	"github.com/rancher/wrangler/pkg/start"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

type appContext struct {
	Fleet    fleetcontrollers.Interface
	Core     corecontrollers.Interface
	CoreNS   corecontrollers.Interface
	RBAC     rbaccontrollers.Interface
	Batch    batchcontrollers.Interface
	Dynamic  dynamic.Interface
	K8s      kubernetes.Interface
	Apply    apply.Apply
	starters []start.Starter

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

func Register(ctx context.Context, fleetNamespace, namespace string, fleetConfig *rest.Config, clientConfig clientcmd.ClientConfig) error {
	appCtx, err := newContext(fleetNamespace, namespace, fleetConfig, clientConfig)
	if err != nil {
		return err
	}

	helmDeployer, err := helmdeployer.NewHelm(namespace, appCtx)
	if err != nil {
		return err
	}

	bundledeployment.Register(ctx,
		trigger.New(ctx, appCtx.restMapper, appCtx.Dynamic),
		deployer.NewManager(
			fleetNamespace,
			appCtx.Fleet.BundleDeployment().Cache(),
			manifest.NewLookup(appCtx.Fleet.Content()),
			helmDeployer,
			appCtx.Apply),
		appCtx.Fleet.BundleDeployment())

	secret.Register(ctx, namespace, appCtx.CoreNS.Secret())

	leader.RunOrDie(ctx, namespace, "fleet-agent", appCtx.K8s, func(ctx context.Context) {
		if err := appCtx.start(ctx); err != nil {
			logrus.Fatal(err)
		}
	})

	return nil
}

func newContext(fleetNamespace, namespace string, fleetConfig *rest.Config, clientConfig clientcmd.ClientConfig) (*appContext, error) {
	client, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	coreNSed, err := core.NewFactoryFromConfigWithNamespace(client, namespace)
	if err != nil {
		return nil, err
	}
	coreNSv := coreNSed.Core().V1()

	core, err := core.NewFactoryFromConfig(client)
	if err != nil {
		return nil, err
	}
	corev := core.Core().V1()

	fleet, err := fleet.NewFactoryFromConfigWithOptions(fleetConfig, &fleet.FactoryOptions{
		Namespace: fleetNamespace,
		Resync:    30 * time.Minute,
	})
	if err != nil {
		return nil, err
	}
	fleetv := fleet.Fleet().V1alpha1()

	rbac, err := rbac.NewFactoryFromConfig(client)
	if err != nil {
		return nil, err
	}
	rbacv := rbac.Rbac().V1()

	batch, err := batch2.NewFactoryFromConfig(client)
	if err != nil {
		return nil, err
	}
	batchv := batch.Batch().V1()

	apply, err := apply.NewForConfig(client)
	if err != nil {
		return nil, err
	}

	dynamic, err := dynamic.NewForConfig(client)
	if err != nil {
		return nil, err
	}

	k8s, err := kubernetes.NewForConfig(client)
	if err != nil {
		return nil, err
	}

	cache := memory.NewMemCacheClient(k8s.Discovery())

	return &appContext{
		Dynamic: dynamic,
		Apply:   apply,
		Fleet:   fleetv,
		Core:    corev,
		CoreNS:  coreNSv,
		Batch:   batchv,
		RBAC:    rbacv,
		K8s:     k8s,

		clientConfig:             clientConfig,
		restConfig:               client,
		cachedDiscoveryInterface: cache,
		restMapper:               restmapper.NewDeferredDiscoveryRESTMapper(cache),
		starters: []start.Starter{
			core,
			coreNSed,
			fleet,
			rbac,
			batch,
		},
	}, nil
}
