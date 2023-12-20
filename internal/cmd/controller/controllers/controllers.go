// Package controllers sets up the controllers for the fleet-controller.
package controllers

import (
	"context"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/rancher/fleet/internal/cmd/controller/controllers/bundle"
	"github.com/rancher/fleet/internal/cmd/controller/controllers/config"
	"github.com/rancher/fleet/internal/cmd/controller/controllers/display"
	"github.com/rancher/fleet/internal/cmd/controller/controllers/git"
	"github.com/rancher/fleet/internal/cmd/controller/controllers/image"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"

	"github.com/rancher/gitjob/pkg/generated/controllers/gitjob.cattle.io"
	gitcontrollers "github.com/rancher/gitjob/pkg/generated/controllers/gitjob.cattle.io/v1"
	"github.com/rancher/lasso/pkg/cache"
	"github.com/rancher/lasso/pkg/client"
	"github.com/rancher/lasso/pkg/controller"
	"github.com/rancher/wrangler/v2/pkg/apply"
	"github.com/rancher/wrangler/v2/pkg/generated/controllers/apps"
	appscontrollers "github.com/rancher/wrangler/v2/pkg/generated/controllers/apps/v1"
	"github.com/rancher/wrangler/v2/pkg/generated/controllers/core"
	corecontrollers "github.com/rancher/wrangler/v2/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/v2/pkg/generated/controllers/rbac"
	rbaccontrollers "github.com/rancher/wrangler/v2/pkg/generated/controllers/rbac/v1"
	"github.com/rancher/wrangler/v2/pkg/leader"
	"github.com/rancher/wrangler/v2/pkg/start"

	"k8s.io/apimachinery/pkg/api/meta"
	memory "k8s.io/client-go/discovery/cached"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
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
	starters      []start.Starter
}

func (a *appContext) start(ctx context.Context) error {
	return start.All(ctx, 50, a.starters...)
}

func Register(ctx context.Context, systemNamespace string, client *rest.Config, disableGitops bool) error {
	appCtx, err := newContext(client)
	if err != nil {
		return err
	}

	// config should be registered first to ensure the global
	// config is available to all components
	if err := config.Register(ctx,
		systemNamespace,
		appCtx.Core.ConfigMap()); err != nil {
		return err
	}

	bundle.Register(ctx,
		appCtx.Apply,
		appCtx.RESTMapper,
		appCtx.TargetManager,
		appCtx.Bundle(),
		appCtx.Cluster(),
		appCtx.ImageScan(),
		appCtx.GitRepo().Cache(),
		appCtx.BundleDeployment())

	if !disableGitops {
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

	display.Register(ctx,
		appCtx.Cluster(),
		appCtx.ClusterGroup(),
		appCtx.GitRepo(),
		appCtx.BundleDeployment())

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

func ControllerFactory(rest *rest.Config) (controller.SharedControllerFactory, error) {
	rateLimit := workqueue.NewItemExponentialFailureRateLimiter(durations.FailureRateLimiterBase, durations.FailureRateLimiterMax)
	clusterRateLimiter := workqueue.NewItemExponentialFailureRateLimiter(durations.SlowFailureRateLimiterBase, durations.SlowFailureRateLimiterMax)
	clientFactory, err := client.NewSharedClientFactory(rest, nil)
	if err != nil {
		return nil, err
	}

	cacheFactory := cache.NewSharedCachedFactory(clientFactory, nil)
	return controller.NewSharedControllerFactory(cacheFactory, &controller.SharedControllerFactoryOptions{
		DefaultRateLimiter:     rateLimit,
		DefaultWorkers:         50,
		SyncOnlyChangedObjects: true,
		KindRateLimiter: map[schema.GroupVersionKind]workqueue.RateLimiter{
			v1alpha1.SchemeGroupVersion.WithKind("Cluster"): clusterRateLimiter,
		},
	}), nil
}

func newContext(client *rest.Config) (*appContext, error) {
	scf, err := ControllerFactory(client)
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
		starters: []start.Starter{
			core,
			apps,
			fleet,
			rbac,
			git,
		},
	}, nil
}
