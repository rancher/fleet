// Package fleetcontroller registers the fleet controller. (fleetcontroller)
package fleetcontroller

import (
	"context"

	"github.com/rancher/fleet/pkg/controllers"
	"github.com/rancher/fleet/pkg/crd"
	"github.com/rancher/wrangler/pkg/kubeconfig"
	"github.com/rancher/wrangler/pkg/ratelimit"
)

func Start(ctx context.Context, systemNamespace string, kubeconfigFile string, disableGitops bool) error {
	cfg := kubeconfig.GetNonInteractiveClientConfig(kubeconfigFile)
	clientConfig, err := cfg.ClientConfig()
	if err != nil {
		return err
	}

	clientConfig.RateLimiter = ratelimit.None

	if err := crd.Create(ctx, clientConfig); err != nil {
		return err
	}

	return controllers.Register(ctx, systemNamespace, cfg, disableGitops)
}
