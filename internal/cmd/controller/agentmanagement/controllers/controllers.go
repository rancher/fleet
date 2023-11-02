package controllers

import (
	"context"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/bootstrap"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/cluster"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/clustergroup"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/clusterregistration"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/clusterregistrationtoken"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/manageagent"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/resources"
	"github.com/rancher/fleet/internal/cmd/controller/controllers/config"
	fleetns "github.com/rancher/fleet/internal/cmd/controller/namespace"

	"github.com/rancher/fleet/internal/cmd/controller/controllers"
	"github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	fleetcontrollers "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v2/pkg/apply"
	"github.com/rancher/wrangler/v2/pkg/generated/controllers/core"
	corecontrollers "github.com/rancher/wrangler/v2/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/v2/pkg/generated/controllers/rbac"
	rbaccontrollers "github.com/rancher/wrangler/v2/pkg/generated/controllers/rbac/v1"
	"github.com/rancher/wrangler/v2/pkg/ratelimit"
	"github.com/rancher/wrangler/v2/pkg/start"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type AppContext struct {
	fleetcontrollers.Interface

	K8s          kubernetes.Interface
	Core         corecontrollers.Interface
	RBAC         rbaccontrollers.Interface
	RESTMapper   meta.RESTMapper
	Apply        apply.Apply
	ClientConfig clientcmd.ClientConfig
	starters     []start.Starter
}

func (a *AppContext) Start(ctx context.Context) error {
	return start.All(ctx, 50, a.starters...)
}

func Register(ctx context.Context, appCtx *AppContext, systemNamespace string, disableBootstrap bool) error {
	systemRegistrationNamespace := fleetns.SystemRegistrationNamespace(systemNamespace)

	// config should be registered first to ensure the global
	// config is available to all components
	if err := config.Register(ctx,
		systemNamespace,
		appCtx.Core.ConfigMap()); err != nil {
		return err
	}

	if err := resources.ApplyBootstrapResources(
		systemNamespace,
		systemRegistrationNamespace,
		appCtx.Apply.
			WithSetID("fleet-bootstrap-data").
			WithDynamicLookup().
			WithNoDeleteGVK(fleetns.GVK()),
	); err != nil {
		return err
	}
	if !disableBootstrap {
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
	}

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

	clustergroup.Register(ctx,
		appCtx.Cluster(),
		appCtx.ClusterGroup())

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

	manageagent.Register(ctx,
		systemNamespace,
		appCtx.Apply,
		appCtx.Core.Namespace(),
		appCtx.Cluster(),
		appCtx.Bundle())

	if err := appCtx.Start(ctx); err != nil {
		logrus.Fatal(err)
	}

	return nil
}

func NewAppContext(cfg clientcmd.ClientConfig) (*AppContext, error) {
	client, err := cfg.ClientConfig()
	if err != nil {
		return nil, err
	}
	client.RateLimiter = ratelimit.None

	scf, err := controllers.ControllerFactory(client)
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

	apply, err := apply.NewForConfig(client)
	if err != nil {
		return nil, err
	}
	apply = apply.WithSetOwnerReference(false, false)

	k8s, err := kubernetes.NewForConfig(client)
	if err != nil {
		return nil, err
	}

	return &AppContext{
		K8s:          k8s,
		Interface:    fleetv,
		Core:         corev,
		RBAC:         rbacv,
		Apply:        apply,
		ClientConfig: cfg,
		starters: []start.Starter{
			core,
			fleet,
			rbac,
		},
	}, nil
}
