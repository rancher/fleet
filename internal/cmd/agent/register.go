package agent

import (
	"context"
	"fmt"

	"github.com/rancher/fleet/internal/cmd/agent/register"

	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type Register struct {
	// system namespace is the namespace, the agent runs in, e.g. cattle-fleet-system
	Namespace string
}

func (r *Register) RegisterAgent(ctx context.Context, localConfig *rest.Config) (*register.AgentInfo, error) {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(zopts)))
	ctx = log.IntoContext(ctx, ctrl.Log)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// try to register with upstream fleet controller by obtaining
	// a kubeconfig for the upstream cluster
	agentInfo, err := register.Register(ctx, r.Namespace, localConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to register with upstream cluster: %w", err)
	}

	ns, _, err := agentInfo.ClientConfig.Namespace()
	if err != nil {
		return nil, fmt.Errorf("failed to get namespace from upstream cluster: %w", err)
	}

	_, err = agentInfo.ClientConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig from upstream cluster: %w", err)
	}

	setupLog.Info("successfully registered with upstream cluster", "namespace", ns)

	return agentInfo, nil
}
