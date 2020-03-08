package controllers

import (
	"context"

	"github.com/rancher/fleet/pkg/manifest"

	"github.com/rancher/wrangler-api/pkg/generated/controllers/apps"

	"github.com/rancher/fleet/pkg/controllers/bundle"
	"github.com/rancher/fleet/pkg/controllers/cleanup"
	"github.com/rancher/fleet/pkg/controllers/cluster"
	"github.com/rancher/fleet/pkg/controllers/clustergroup"
	"github.com/rancher/fleet/pkg/controllers/clustergrouptoken"
	"github.com/rancher/fleet/pkg/controllers/clusterregistration"
	"github.com/rancher/fleet/pkg/controllers/config"
	"github.com/rancher/fleet/pkg/controllers/manageagent"
	"github.com/rancher/fleet/pkg/controllers/role"
	"github.com/rancher/fleet/pkg/controllers/serviceaccount"
	"github.com/rancher/fleet/pkg/controllers/sharedindex"
	"github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/target"
	appscontrollers "github.com/rancher/wrangler-api/pkg/generated/controllers/apps/v1"
	"github.com/rancher/wrangler-api/pkg/generated/controllers/core"
	corecontrollers "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler-api/pkg/generated/controllers/rbac"
	rbaccontrollers "github.com/rancher/wrangler-api/pkg/generated/controllers/rbac/v1"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/start"
	"k8s.io/client-go/rest"
)

type appContext struct {
	fleetcontrollers.Interface

	Core          corecontrollers.Interface
	Apps          appscontrollers.Interface
	RBAC          rbaccontrollers.Interface
	TargetManager *target.Manager
	Apply         apply.Apply
	starters      []start.Starter
}

func (a *appContext) start(ctx context.Context) error {
	return start.All(ctx, 5, a.starters...)
}

func Register(ctx context.Context, client *rest.Config) error {
	appCtx, err := newContext(client)
	if err != nil {
		return err
	}

	// config should be registered first to ensure the global
	// config is available to all components
	if err := config.Register(ctx,
		appCtx.Apply,
		appCtx.Core.ConfigMap()); err != nil {
		return err
	}

	clusterregistration.Register(ctx,
		appCtx.Apply,
		appCtx.Core.ServiceAccount(),
		appCtx.RBAC.Role(),
		appCtx.RBAC.RoleBinding(),
		appCtx.RBAC.ClusterRole(),
		appCtx.RBAC.ClusterRoleBinding(),
		appCtx.ClusterRegistrationRequest(),
		appCtx.Cluster().Cache(),
		appCtx.ClusterGroup().Cache(),
		appCtx.Cluster())

	serviceaccount.Register(ctx,
		appCtx.Apply,
		appCtx.RBAC.Role(),
		appCtx.RBAC.RoleBinding(),
		appCtx.Core.ServiceAccount(),
		appCtx.ClusterRegistrationRequest(),
		appCtx.Cluster().Cache(),
		appCtx.Core.Secret(),
		appCtx.ClusterGroup().Cache())

	cluster.Register(ctx,
		appCtx.BundleDeployment(),
		appCtx.ClusterGroup().Cache(),
		appCtx.Cluster(),
		appCtx.Core.Namespace(),
		appCtx.Apply)

	bundle.Register(ctx,
		appCtx.Apply,
		appCtx.TargetManager,
		appCtx.Bundle(),
		appCtx.Cluster(),
		appCtx.BundleDeployment())

	clustergroup.Register(ctx,
		appCtx.Apply,
		appCtx.Core.Namespace(),
		appCtx.RBAC.Role(),
		appCtx.Cluster().Cache(),
		appCtx.ClusterGroup(),
		appCtx.Cluster())

	role.Register(ctx,
		appCtx.Core.Secret(),
		appCtx.RBAC.Role(),
		appCtx.ClusterGroup())

	clustergrouptoken.Register(ctx,
		appCtx.Apply,
		appCtx.ClusterGroupToken(),
		appCtx.ClusterGroup().Cache(),
		appCtx.Core.ServiceAccount())

	cleanup.Register(ctx,
		appCtx.Apply,
		appCtx.Core.Secret(),
		appCtx.Core.ServiceAccount(),
		appCtx.RBAC.Role(),
		appCtx.RBAC.RoleBinding(),
		appCtx.RBAC.ClusterRole(),
		appCtx.RBAC.ClusterRoleBinding(),
		appCtx.ClusterGroupToken(),
		appCtx.ClusterRegistrationRequest(),
		appCtx.ClusterGroup(),
		appCtx.Cluster(),
		appCtx.Core.Namespace())

	manageagent.Register(ctx,
		appCtx.Apply,
		appCtx.ClusterGroup(),
		appCtx.Bundle())

	sharedindex.Register(ctx,
		appCtx.ClusterGroup().Cache())

	return appCtx.start(ctx)
}

func newContext(client *rest.Config) (*appContext, error) {
	core, err := core.NewFactoryFromConfig(client)
	if err != nil {
		return nil, err
	}
	corev := core.Core().V1()

	fleet, err := fleet.NewFactoryFromConfig(client)
	if err != nil {
		return nil, err
	}
	fleetv := fleet.Fleet().V1alpha1()

	rbac, err := rbac.NewFactoryFromConfig(client)
	if err != nil {
		return nil, err
	}
	rbacv := rbac.Rbac().V1()

	apps, err := apps.NewFactoryFromConfig(client)
	if err != nil {
		return nil, err
	}
	appsv := apps.Apps().V1()

	apply, err := apply.NewForConfig(client)
	if err != nil {
		return nil, err
	}

	targetManager := target.New(
		fleetv.Cluster().Cache(),
		fleetv.ClusterGroup().Cache(),
		fleetv.Bundle().Cache(),
		manifest.NewStore(fleetv.Content()),
		fleetv.BundleDeployment().Cache())

	return &appContext{
		Apps:          appsv,
		Interface:     fleetv,
		Core:          corev,
		RBAC:          rbacv,
		Apply:         apply,
		TargetManager: targetManager,
		starters: []start.Starter{
			core,
			apps,
			fleet,
			rbac,
		},
	}, nil
}
