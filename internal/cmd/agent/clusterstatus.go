package agent

import (
	"context"
	"fmt"
	"os"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/rancher/fleet/internal/cmd/agent/clusterstatus"
	"github.com/rancher/fleet/internal/cmd/agent/register"
)

type ClusterStatus struct {
	UpstreamOptions
	CheckinInterval string `usage:"How often to post cluster status" env:"CHECKIN_INTERVAL"`
	AgentInfo       *register.AgentInfo
}

func (cs *ClusterStatus) Start(ctx context.Context) error {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(zopts)))
	ctx = log.IntoContext(ctx, ctrl.Log)

	var err error
	var checkinInterval time.Duration
	if cs.CheckinInterval != "" {
		checkinInterval, err = time.ParseDuration(cs.CheckinInterval)
		if err != nil {
			return err
		}
	}

	localConfig, err := clientcmd.BuildConfigFromFlags("", cs.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// without rate limiting
	localConfig = rest.CopyConfig(localConfig)
	localConfig.QPS = -1
	localConfig.RateLimiter = nil

	setupLog.Info("Fetching kubeconfig for upstream cluster from registration", "namespace", cs.Namespace)

	clnt, err := client.New(localConfig, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		fmt.Println("failed to create client")
		os.Exit(1)
	}

	setupLog.Info("Starting cluster status ticker", "checkin interval", checkinInterval.String(), "cluster namespace", cs.AgentInfo.ClusterNamespace, "cluster name", cs.AgentInfo.ClusterName)

	go func() {
		clusterstatus.Ticker(
			ctx,
			clnt,
			cs.Namespace,
			cs.AgentInfo.ClusterNamespace,
			cs.AgentInfo.ClusterName,
			checkinInterval,
		)

		<-ctx.Done()
	}()

	return nil
}
