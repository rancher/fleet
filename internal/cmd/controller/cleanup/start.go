package cleanup

import (
	"context"

	"github.com/rancher/fleet/internal/cmd/controller/cleanup/controllers"
	"github.com/rancher/wrangler/v3/pkg/kubeconfig"
	"github.com/rancher/wrangler/v3/pkg/leader"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func start(ctx context.Context, kubeConfig, namespace string) error {
	clientConfig := kubeconfig.GetNonInteractiveClientConfig(kubeConfig)
	kc, err := clientConfig.ClientConfig()
	if err != nil {
		return err
	}

	localConfig := rest.CopyConfig(kc)
	k8s, err := kubernetes.NewForConfig(localConfig)
	if err != nil {
		return err
	}

	leader.RunOrDie(ctx, namespace, "fleet-cleanup-lock", k8s, func(ctx context.Context) {
		appCtx, err := controllers.NewAppContext(clientConfig)
		if err != nil {
			logrus.Fatal(err)
		}
		if err := controllers.Register(ctx, appCtx); err != nil {
			logrus.Fatal(err)
		}
	})

	return nil
}
