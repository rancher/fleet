package agentmanagement

import (
	"context"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers"

	"github.com/rancher/wrangler/v3/pkg/kubeconfig"
	"github.com/rancher/wrangler/v3/pkg/leader"
	"github.com/rancher/wrangler/v3/pkg/ratelimit"
	"github.com/rancher/wrangler/v3/pkg/schemes"

	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/apps/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func start(ctx context.Context, kubeConfig, namespace string, disableBootstrap bool) error {
	clientConfig := kubeconfig.GetNonInteractiveClientConfig(kubeConfig)
	kc, err := clientConfig.ClientConfig()
	if err != nil {
		return err
	}

	// try to claim leadership lease without rate limiting
	localConfig := rest.CopyConfig(kc)
	localConfig.RateLimiter = ratelimit.None
	k8s, err := kubernetes.NewForConfig(localConfig)
	if err != nil {
		return err
	}

	err = schemes.Register(v1.AddToScheme)
	if err != nil {
		return err
	}

	leader.RunOrDie(ctx, namespace, "fleet-agentmanagement-lock", k8s, func(ctx context.Context) {
		appCtx, err := controllers.NewAppContext(clientConfig)
		if err != nil {
			logrus.Fatal(err)
		}
		if err := controllers.Register(ctx, appCtx, namespace, disableBootstrap); err != nil {
			logrus.Fatal(err)
		}
		if err := appCtx.Start(ctx); err != nil {
			logrus.Fatal(err)
		}
	})

	return nil
}
