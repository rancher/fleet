package agent

import (
	"context"

	"github.com/rancher/fleet/modules/agent/pkg/controllers"
	"github.com/rancher/fleet/modules/agent/pkg/register"
	"github.com/rancher/wrangler/pkg/kubeconfig"
)

func Start(ctx context.Context, kubeConfig, namespace string) error {
	clientConfig := kubeconfig.GetNonInteractiveClientConfig(kubeConfig)
	kc, err := clientConfig.ClientConfig()
	if err != nil {
		return err
	}

	fleetClientConfig, err := register.Register(ctx, namespace, kc)
	if err != nil {
		return err
	}

	fleetNamespace, _, err := fleetClientConfig.Namespace()
	if err != nil {
		return err
	}

	fleetRestConfig, err := fleetClientConfig.ClientConfig()
	if err != nil {
		return err
	}

	return controllers.Register(ctx, fleetNamespace, namespace, fleetRestConfig, clientConfig)
}
