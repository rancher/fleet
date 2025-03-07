package agent

import (
	"context"

	"github.com/rancher/fleet/internal/cmd/agent/register"

	"github.com/rancher/wrangler/v3/pkg/kubeconfig"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type Register struct {
	UpstreamOptions
}

func (r *Register) RegisterAgent(ctx context.Context) error {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(zopts)))
	ctx = log.IntoContext(ctx, ctrl.Log)

	clientConfig := kubeconfig.GetNonInteractiveClientConfig(r.Kubeconfig)
	kc, err := clientConfig.ClientConfig()
	if err != nil {
		return err
	}

	setupLog.Info("starting registration on upstream cluster", "namespace", r.Namespace)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// try to register with upstream fleet controller by obtaining
	// a kubeconfig for the upstream cluster
	agentInfo, err := register.Register(ctx, r.Namespace, kc)
	if err != nil {
		setupLog.Error(err, "failed to register with upstream cluster")
		return err
	}

	ns, _, err := agentInfo.ClientConfig.Namespace()
	if err != nil {
		setupLog.Error(err, "failed to get namespace from upstream cluster")
		return err
	}

	_, err = agentInfo.ClientConfig.ClientConfig()
	if err != nil {
		setupLog.Error(err, "failed to get kubeconfig from upstream cluster")
		return err
	}

	setupLog.Info("successfully registered with upstream cluster", "namespace", ns)

	return nil
}
