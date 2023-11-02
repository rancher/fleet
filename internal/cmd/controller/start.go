package controller

import (
	"context"

	"github.com/rancher/fleet/internal/cmd/controller/controllers"
	"github.com/rancher/wrangler/v2/pkg/kubeconfig"
	"github.com/rancher/wrangler/v2/pkg/ratelimit"
)

func start(ctx context.Context, systemNamespace string, kubeconfigFile string, disableGitops bool) error {
	cfg := kubeconfig.GetNonInteractiveClientConfig(kubeconfigFile)
	clientConfig, err := cfg.ClientConfig()
	if err != nil {
		return err
	}

	clientConfig.RateLimiter = ratelimit.None

	return controllers.Register(ctx, systemNamespace, cfg, disableGitops)
}
