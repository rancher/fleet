package controllers

import (
	"context"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/bootstrap"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/cluster"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/clusterregistration"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/clusterregistrationtoken"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/config"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/manageagent"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/resources"
	fleetns "github.com/rancher/fleet/internal/cmd/controller/namespace"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// NewControllerRuntimeMgrFromConfig creates a new controller-runtime manager from rest config
// with leader election enabled for HA deployments
func NewControllerRuntimeMgrFromConfig(cfg *rest.Config, namespace string) (ctrl.Manager, error) {
	// Use the scheme from manager.go which has all necessary types registered
	scheme := ctrlScheme

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
		HealthProbeBindAddress:  "0",
		LeaderElection:          true,
		LeaderElectionID:        "fleet-agentmanagement-lock",
		LeaderElectionNamespace: namespace,
	})
	if err != nil {
		return nil, err
	}

	// Add health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return nil, err
	}

	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return nil, err
	}

	return mgr, nil
}

// RegisterAll registers all agentmanagement controllers using controller-runtime
func RegisterAll(ctx context.Context, mgr ctrl.Manager, restConfig *rest.Config, clientConfig clientcmd.ClientConfig, systemNamespace string, disableBootstrap bool) error {
	systemRegistrationNamespace := fleetns.SystemRegistrationNamespace(systemNamespace)

	// config should be registered first to ensure the global config is available to all components
	if err := config.Register(ctx, mgr, systemNamespace); err != nil {
		return err
	}

	// Apply bootstrap resources using controller-runtime client
	if err := resources.ApplyBootstrapResources(
		ctx,
		mgr.GetClient(),
		systemNamespace,
		systemRegistrationNamespace,
	); err != nil {
		return err
	}

	if !disableBootstrap {
		if err := bootstrap.Register(ctx, mgr, systemNamespace, clientConfig); err != nil {
			return err
		}
	}

	if err := cluster.Register(mgr, systemNamespace); err != nil {
		return err
	}

	if err := clusterregistration.RegisterControllerRuntime(
		mgr,
		systemNamespace,
		systemRegistrationNamespace,
	); err != nil {
		return err
	}

	if err := clusterregistrationtoken.RegisterControllerRuntime(
		mgr,
		systemNamespace,
		systemRegistrationNamespace,
	); err != nil {
		return err
	}

	if err := manageagent.RegisterControllerRuntime(mgr, systemNamespace); err != nil {
		return err
	}

	logrus.Info("All agentmanagement controllers registered successfully")
	return nil
}

// StartControllerRuntimeManager starts the controller-runtime manager
func StartControllerRuntimeManager(ctx context.Context, mgr ctrl.Manager) error {
	logrus.Info("Starting controller-runtime manager")
	return mgr.Start(ctx)
}
