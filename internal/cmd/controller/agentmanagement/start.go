package agentmanagement

import (
	"context"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers"
	"github.com/rancher/wrangler/v3/pkg/kubeconfig"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/rest"
)

func start(ctx context.Context, kubeConfig, namespace string, disableBootstrap bool) error {
	clientConfig := kubeconfig.GetNonInteractiveClientConfig(kubeConfig)
	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return err
	}

	// Disable rate limiting for API server communication
	localConfig := rest.CopyConfig(restConfig)
	localConfig.QPS = -1
	localConfig.RateLimiter = nil

	// Create controller-runtime manager with built-in leader election
	mgr, err := controllers.NewControllerRuntimeMgrFromConfig(localConfig, namespace)
	if err != nil {
		logrus.Fatal(err)
	}

	// Register all controllers
	if err := controllers.RegisterAll(ctx, mgr, localConfig, clientConfig, namespace, disableBootstrap); err != nil {
		logrus.Fatal(err)
	}

	// Start the manager (includes leader election)
	if err := controllers.StartControllerRuntimeManager(ctx, mgr); err != nil {
		logrus.Fatal(err)
	}

	return nil
}
