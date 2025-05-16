package agent

import (
	"context"
	"time"

	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/rancher/fleet/internal/cmd/agent/clusterstatus"
	"github.com/rancher/fleet/internal/cmd/agent/register"
)

type ClusterStatusRunnable struct {
	config          *rest.Config
	namespace       string
	checkinInterval string
	agentInfo       *register.AgentInfo
}

func (cs *ClusterStatusRunnable) Start(ctx context.Context) error {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(zopts)))
	ctx = log.IntoContext(ctx, ctrl.Log)

	var err error
	var checkinInterval time.Duration
	if cs.checkinInterval != "" {
		checkinInterval, err = time.ParseDuration(cs.checkinInterval)
		if err != nil {
			return err
		}
	}

	setupLog.Info("Starting cluster status ticker", "checkin interval", checkinInterval.String(), "cluster namespace", cs.agentInfo.ClusterNamespace, "cluster name", cs.agentInfo.ClusterName)

	// use a separate client for the cluster status ticker, that does not use a cache
	client, err := client.New(cs.config, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	go func() {
		clusterstatus.Ticker(
			ctx,
			client,
			cs.namespace,
			cs.agentInfo.ClusterNamespace,
			cs.agentInfo.ClusterName,
			checkinInterval,
		)

		<-ctx.Done()
	}()

	return nil
}
