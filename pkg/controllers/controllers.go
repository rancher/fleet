// Package controllers sets up the controllers for the fleet-controller. (fleetcontroller)
package controllers

import (
	"context"

	"github.com/sirupsen/logrus"

	"github.com/rancher/fleet/pkg/controllers/bootstrap"
	"github.com/rancher/fleet/pkg/controllers/bundle"
	"github.com/rancher/fleet/pkg/controllers/cleanup"
	"github.com/rancher/fleet/pkg/controllers/cluster"
	"github.com/rancher/fleet/pkg/controllers/clustergroup"
	"github.com/rancher/fleet/pkg/controllers/clusterregistration"
	"github.com/rancher/fleet/pkg/controllers/clusterregistrationtoken"
	"github.com/rancher/fleet/pkg/controllers/config"
	"github.com/rancher/fleet/pkg/controllers/content"
	"github.com/rancher/fleet/pkg/controllers/display"
	"github.com/rancher/fleet/pkg/controllers/git"
	"github.com/rancher/fleet/pkg/controllers/image"
	"github.com/rancher/fleet/pkg/controllers/manageagent"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/manifest"
	fleetns "github.com/rancher/fleet/pkg/namespace"
	"github.com/rancher/fleet/pkg/target"

	"github.com/rancher/gitjob/pkg/generated/controllers/gitjob.cattle.io"
	gitcontrollers "github.com/rancher/gitjob/pkg/generated/controllers/gitjob.cattle.io/v1"
	"github.com/rancher/lasso/pkg/cache"
	"github.com/rancher/lasso/pkg/client"
	"github.com/rancher/lasso/pkg/controller"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/generated/controllers/apps"
	appscontrollers "github.com/rancher/wrangler/pkg/generated/controllers/apps/v1"
	"github.com/rancher/wrangler/pkg/generated/controllers/core"
	corecontrollers "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/generated/controllers/rbac"
	rbaccontrollers "github.com/rancher/wrangler/pkg/generated/controllers/rbac/v1"
	"github.com/rancher/wrangler/pkg/leader"
	"github.com/rancher/wrangler/pkg/ratelimit"
	"github.com/rancher/wrangler/pkg/start"

	"k8s.io/apimachinery/pkg/api/meta"
	memory "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"
)

type appContext struct {
	fleetcontrollers.Interface

	K8s           kubernetes.Interface
	Core          corecontrollers.Interface
	Apps          appscontrollers.Interface
	RBAC          rbaccontrollers.Interface
	GitJob        gitcontrollers.Interface
	TargetManager *target.Manager
	RESTMapper    meta.RESTMapper
	Apply         apply.Apply
	ClientConfig  clientcmd.ClientConfig
	starters      []start.Starter
	DisableGitops bool
}

func (a *appContext) start(ctx context.Context) error {
	return start.All(ctx, 50, a.starters...)
}

func Register(ctx context.Context, systemNamespace string, cfg clientcmd.ClientConfig, disableGitops bool) error {
	appCtx, err := newContext(cfg, disableGitops)
	if err != nil {
		return err
	}

	systemRegistrationNamespace := fleetns.RegistrationNamespace(systemNamespace)

	if err := addData(systemNamespace, systemRegistrationNamespace, appCtx); err != nil {
		return err
	}

	// config should be registered first to ensure the global
	// config is available to all components
	if err := config.Register(ctx,
		systemNamespace,
		appCtx.Core.ConfigMap()); err != nil {
		return err
	}

	clusterregistration.Register(ctx,
		appCtx.Apply.WithCacheTypes(
			appCtx.RBAC.ClusterRole(),
			appCtx.RBAC.ClusterRoleBinding(),
		),
		systemNamespace,
		systemRegistrationNamespace,
		appCtx.Core.ServiceAccount(),
		appCtx.Core.Secret(),
		appCtx.RBAC.Role(),
		appCtx.RBAC.RoleBinding(),
		appCtx.ClusterRegistration(),
		appCtx.Cluster())

	cluster.Register(ctx,
		appCtx.BundleDeployment(),
		appCtx.ClusterGroup().Cache(),
		appCtx.Cluster(),
		appCtx.GitRepo().Cache(),
		appCtx.Core.Namespace(),
		appCtx.ClusterRegistration())

	cluster.RegisterImport(ctx,
		systemNamespace,
		appCtx.Core.Secret().Cache(),
		appCtx.Cluster(),
		appCtx.ClusterRegistrationToken(),
		appCtx.Bundle(),
		appCtx.Core.Namespace())

	bundle.Register(ctx,
		appCtx.Apply,
		appCtx.RESTMapper,
		appCtx.TargetManager,
		appCtx.Bundle(),
		appCtx.Cluster(),
		appCtx.ImageScan(),
		appCtx.GitRepo().Cache(),
		appCtx.BundleDeployment())

	clustergroup.Register(ctx,
		appCtx.Cluster(),
		appCtx.ClusterGroup())

	content.Register(ctx,
		appCtx.Content(),
		appCtx.BundleDeployment(),
		appCtx.Core.Namespace())

	clusterregistrationtoken.Register(ctx,
		systemNamespace,
		systemRegistrationNamespace,
		appCtx.Apply.WithCacheTypes(
			appCtx.Core.Secret(),
			appCtx.Core.ServiceAccount(),
			appCtx.RBAC.Role(),
			appCtx.RBAC.RoleBinding()),
		appCtx.ClusterRegistrationToken(),
		appCtx.Core.ServiceAccount(),
		appCtx.Core.Secret().Cache(),
		appCtx.Core.Secret())

	cleanup.Register(ctx,
		appCtx.Apply.WithCacheTypes(
			appCtx.Core.Secret(),
			appCtx.Core.ServiceAccount(),
			appCtx.RBAC.Role(),
			appCtx.RBAC.RoleBinding(),
			appCtx.RBAC.ClusterRole(),
			appCtx.RBAC.ClusterRoleBinding(),
			appCtx.Bundle(),
			appCtx.ClusterRegistrationToken(),
			appCtx.ClusterRegistration(),
			appCtx.ClusterGroup(),
			appCtx.Cluster(),
			appCtx.Core.Namespace()),
		appCtx.Core.Secret(),
		appCtx.Core.ServiceAccount(),
		appCtx.BundleDeployment(),
		appCtx.RBAC.Role(),
		appCtx.RBAC.RoleBinding(),
		appCtx.RBAC.ClusterRole(),
		appCtx.RBAC.ClusterRoleBinding(),
		appCtx.Core.Namespace(),
		appCtx.Cluster().Cache())

	manageagent.Register(ctx,
		systemNamespace,
		appCtx.Apply,
		appCtx.Core.Namespace(),
		appCtx.Cluster(),
		appCtx.Bundle())

	if !appCtx.DisableGitops {
		git.Register(ctx,
			appCtx.Apply.WithCacheTypes(
				appCtx.RBAC.Role(),
				appCtx.RBAC.RoleBinding(),
				appCtx.GitJob.GitJob(),
				appCtx.Core.ConfigMap(),
				appCtx.Core.ServiceAccount()),
			appCtx.GitJob.GitJob(),
			appCtx.BundleDeployment(),
			appCtx.GitRepoRestriction().Cache(),
			appCtx.Bundle(),
			appCtx.ImageScan(),
			appCtx.GitRepo(),
			appCtx.Core.Secret().Cache())
	}

	bootstrap.Register(ctx,
		systemNamespace,
		appCtx.Apply.WithCacheTypes(
			appCtx.GitRepo(),
			appCtx.Cluster(),
			appCtx.ClusterGroup(),
			appCtx.Core.Namespace(),
			appCtx.Core.Secret()),
		appCtx.ClientConfig,
		appCtx.Core.ServiceAccount().Cache(),
		appCtx.Core.Secret(),
		appCtx.Core.Secret().Cache())

	display.Register(ctx,
		appCtx.Cluster(),
		appCtx.ClusterGroup(),
		appCtx.GitRepo(),
		appCtx.BundleDeployment(),
		appCtx.Bundle())

	image.Register(ctx,
		appCtx.Core,
		appCtx.GitRepo(),
		appCtx.ImageScan())

	leader.RunOrDie(ctx, systemNamespace, "fleet-controller-lock", appCtx.K8s, func(ctx context.Context) {
		if err := appCtx.start(ctx); err != nil {
			logrus.Fatal(err)
		}
		logrus.Info("All controllers have been started")
	})

	return nil
}

func controllerFactory(rest *rest.Config) (controller.SharedControllerFactory, error) {
	rateLimit := workqueue.NewItemExponentialFailureRateLimiter(durations.FailureRateLimiterBase, durations.FailureRateLimiterMax)
	workqueue.DefaultControllerRateLimiter()
	clientFactory, err := client.NewSharedClientFactory(rest, nil)
	if err != nil {
		return nil, err
	}

	cacheFactory := cache.NewSharedCachedFactory(clientFactory, nil)
	return controller.NewSharedControllerFactory(cacheFactory, &controller.SharedControllerFactoryOptions{
		DefaultRateLimiter:     rateLimit,
		DefaultWorkers:         50,
		SyncOnlyChangedObjects: true,
	}), nil
}

func newContext(cfg clientcmd.ClientConfig, disableGitops bool) (*appContext, error) {
	client, err := cfg.ClientConfig()
	if err != nil {
		return nil, err
	}
	client.RateLimiter = ratelimit.None

	scf, err := controllerFactory(client)
	if err != nil {
		return nil, err
	}

	core, err := core.NewFactoryFromConfigWithOptions(client, &core.FactoryOptions{
		SharedControllerFactory: scf,
	})
	if err != nil {
		return nil, err
	}
	corev := core.Core().V1()

	fleet, err := fleet.NewFactoryFromConfigWithOptions(client, &fleet.FactoryOptions{
		SharedControllerFactory: scf,
	})
	if err != nil {
		return nil, err
	}
	fleetv := fleet.Fleet().V1alpha1()

	rbac, err := rbac.NewFactoryFromConfigWithOptions(client, &rbac.FactoryOptions{
		SharedControllerFactory: scf,
	})
	if err != nil {
		return nil, err
	}
	rbacv := rbac.Rbac().V1()

	apps, err := apps.NewFactoryFromConfigWithOptions(client, &apps.FactoryOptions{
		SharedControllerFactory: scf,
	})
	if err != nil {
		return nil, err
	}
	appsv := apps.Apps().V1()

	git, err := gitjob.NewFactoryFromConfigWithOptions(client, &gitjob.FactoryOptions{
		SharedControllerFactory: scf,
	})
	if err != nil {
		return nil, err
	}
	gitv := git.Gitjob().V1()

	apply, err := apply.NewForConfig(client)
	if err != nil {
		return nil, err
	}
	apply = apply.WithSetOwnerReference(false, false)

	k8s, err := kubernetes.NewForConfig(client)
	if err != nil {
		return nil, err
	}

	mem := memory.NewMemCacheClient(k8s.Discovery())
	restMapper := restmapper.NewDeferredDiscoveryRESTMapper(mem)

	targetManager := target.New(
		fleetv.Cluster().Cache(),
		fleetv.ClusterGroup().Cache(),
		fleetv.Bundle().Cache(),
		fleetv.BundleNamespaceMapping().Cache(),
		corev.Namespace().Cache(),
		manifest.NewStore(fleetv.Content()),
		fleetv.BundleDeployment().Cache())

	return &appContext{
		RESTMapper:    restMapper,
		K8s:           k8s,
		Apps:          appsv,
		Interface:     fleetv,
		Core:          corev,
		RBAC:          rbacv,
		Apply:         apply,
		GitJob:        gitv,
		TargetManager: targetManager,
		ClientConfig:  cfg,
		starters: []start.Starter{
			core,
			apps,
			fleet,
			rbac,
			git,
		},
		DisableGitops: disableGitops,
	}, nil
}
