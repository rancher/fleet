package controller

import (
	"context"

	"github.com/rancher/fleet/internal/cmd/controller/controllers"
	"github.com/rancher/wrangler/v2/pkg/kubeconfig"
	"github.com/rancher/wrangler/v2/pkg/ratelimit"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func start(ctx context.Context, systemNamespace string, kubeconfigFile string, disableGitops bool) error {
	// provide a logger in the context to be compatible with controller-runtime
	zopts := zap.Options{
		Development: true,
	}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))
	ctx = log.IntoContext(ctx, ctrl.Log)

	cfg := kubeconfig.GetNonInteractiveClientConfig(kubeconfigFile)
	clientConfig, err := cfg.ClientConfig()
	if err != nil {
		return err
	}

	clientConfig.RateLimiter = ratelimit.None

	return controllers.Register(ctx, systemNamespace, cfg, disableGitops)
}
