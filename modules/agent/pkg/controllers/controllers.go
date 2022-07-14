package controllers

import (
	"context"
	"fmt"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/chartutil"
	"sort"

	"path"
	"time"

	"github.com/rancher/fleet/modules/agent/pkg/controllers/bundledeployment"
	"github.com/rancher/fleet/modules/agent/pkg/controllers/cluster"
	"github.com/rancher/fleet/modules/agent/pkg/deployer"
	"github.com/rancher/fleet/modules/agent/pkg/trigger"
	"github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/helmdeployer"
	"github.com/rancher/fleet/pkg/manifest"
	cache2 "github.com/rancher/lasso/pkg/cache"
	"github.com/rancher/lasso/pkg/client"
	"github.com/rancher/lasso/pkg/controller"
	"github.com/rancher/wrangler/pkg/apply"
	batch2 "github.com/rancher/wrangler/pkg/generated/controllers/batch"
	batchcontrollers "github.com/rancher/wrangler/pkg/generated/controllers/batch/v1"
	"github.com/rancher/wrangler/pkg/generated/controllers/core"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/generated/controllers/rbac"
	rbaccontrollers "github.com/rancher/wrangler/pkg/generated/controllers/rbac/v1"
	"github.com/rancher/wrangler/pkg/leader"
	"github.com/rancher/wrangler/pkg/ratelimit"
	"github.com/rancher/wrangler/pkg/start"
	"github.com/sirupsen/logrus"
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
	CoreNS   corecontrollers.Interface
	RBAC     rbaccontrollers.Interface
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

	clusterCapabilities, err := GetCapabilities(discovery)
	if err != nil {
		return err
	}
	helmDeployer, err := helmdeployer.NewHelm(agentNamespace, defaultNamespace, labelPrefix, agentScope, appCtx,
		appCtx.Core.ServiceAccount().Cache(), appCtx.Core.ConfigMap().Cache(), appCtx.Core.Secret().Cache(), clusterCapabilities)
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
		appCtx.Fleet.Cluster(),
	)

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
		DefaultResync:    30 * time.Minute,
	}), nil), nil
}

func newContext(fleetNamespace, agentNamespace, clusterNamespace, clusterName string,
	fleetConfig *rest.Config, clientConfig clientcmd.ClientConfig,
	fleetMapper, mapper meta.RESTMapper, discovery discovery.CachedDiscoveryInterface) (*appContext, error) {
	client, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	fleetFactory, err := newSharedControllerFactory(fleetConfig, fleetMapper, fleetNamespace)
	if err != nil {
		return nil, err
	}

	localNSFactory, err := newSharedControllerFactory(client, mapper, agentNamespace)
	if err != nil {
		return nil, err
	}

	localFactory, err := newSharedControllerFactory(client, mapper, "")
	if err != nil {
		return nil, err
	}

	coreNSed, err := core.NewFactoryFromConfigWithOptions(client, &core.FactoryOptions{
		Namespace:               agentNamespace,
		SharedControllerFactory: localNSFactory,
	})
	if err != nil {
		return nil, err
	}
	coreNSv := coreNSed.Core().V1()

	core, err := core.NewFactoryFromConfigWithOptions(client, &core.FactoryOptions{
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

	rbac, err := rbac.NewFactoryFromConfigWithOptions(client, &rbac.FactoryOptions{
		SharedControllerFactory: localFactory,
	})
	if err != nil {
		return nil, err
	}
	rbacv := rbac.Rbac().V1()

	batch, err := batch2.NewFactoryFromConfigWithOptions(client, &batch2.FactoryOptions{
		SharedControllerFactory: localFactory,
	})
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

	client = rest.CopyConfig(client)
	client.RateLimiter = ratelimit.None

	k8s, err := kubernetes.NewForConfig(client)
	if err != nil {
		return nil, err
	}

	return &appContext{
		Dynamic:          dynamic,
		Apply:            apply,
		Fleet:            fleetv,
		Core:             corev,
		CoreNS:           coreNSv,
		Batch:            batchv,
		RBAC:             rbacv,
		K8s:              k8s,
		ClusterNamespace: clusterNamespace,
		ClusterName:      clusterName,
		AgentNamespace:   agentNamespace,

		clientConfig:             clientConfig,
		restConfig:               client,
		cachedDiscoveryInterface: discovery,
		restMapper:               mapper,
		starters: []start.Starter{
			core,
			coreNSed,
			fleet,
			rbac,
			batch,
		},
	}, nil
}

// Taken from Helm capabilities
func GetCapabilities(discovery discovery.CachedDiscoveryInterface) (chartutil.Capabilities, error) {
	discovery.Invalidate()
	kubeVersion, err := discovery.ServerVersion()
	if err != nil {
		return chartutil.Capabilities{}, errors.Wrap(err, "could not get server version from Kubernetes")
	}

	apiVersions, err := GetVersionSet(discovery)
	if err != nil {
		println(err, "could not get server capabilities from Kubernetes")
	}
	clusterCapabilities := chartutil.Capabilities{
		KubeVersion: chartutil.KubeVersion{
			Version: fmt.Sprintf("v%s.%s.0", kubeVersion.Major, kubeVersion.Minor),
			Major:   kubeVersion.Major,
			Minor:   kubeVersion.Minor,
		},
		APIVersions: apiVersions,
	}
	sort.Strings(clusterCapabilities.APIVersions)
	return clusterCapabilities, nil
}

func GetVersionSet(client discovery.ServerResourcesInterface) (chartutil.VersionSet, error) {
	groups, resources, err := client.ServerGroupsAndResources()
	if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
		return []string{}, errors.Wrap(err, "could not get apiVersions from Kubernetes")
	}

	// FIXME: The Kubernetes test fixture for cli appears to always return nil
	// for calls to Discovery().ServerGroupsAndResources(). So in this case, we
	// return the default API list. This is also a safe value to return in any
	// other odd-ball case.
	if len(groups) == 0 && len(resources) == 0 {
		return []string{}, nil
	}

	versionMap := make(map[string]interface{})
	versions := []string{}

	// Extract the groups
	for _, g := range groups {
		for _, gv := range g.Versions {
			versionMap[gv.GroupVersion] = struct{}{}
		}
	}

	// Extract the resources
	var id string
	var ok bool
	for _, r := range resources {
		for _, rl := range r.APIResources {

			// A Kind at a GroupVersion can show up more than once. We only want
			// it displayed once in the final output.
			id = path.Join(r.GroupVersion, rl.Kind)
			if _, ok = versionMap[id]; !ok {
				versionMap[id] = struct{}{}
			}
		}
	}

	// Convert to a form that NewVersionSet can use
	for k := range versionMap {
		versions = append(versions, k)
	}

	return versions, nil
}
