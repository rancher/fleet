package agent

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/rancher/fleet/internal/cmd/agent/clusterstatus"
	"github.com/rancher/fleet/internal/cmd/agent/register"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"

	cache2 "github.com/rancher/lasso/pkg/cache"
	"github.com/rancher/lasso/pkg/client"
	"github.com/rancher/lasso/pkg/controller"
	"github.com/rancher/lasso/pkg/mapper"
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

	// cannot start without kubeconfig for upstream cluster
	setupLog.Info("Fetching kubeconfig for upstream cluster from registration", "namespace", cs.Namespace)

	// set up factory for upstream cluster
	fleetNamespace, _, err := cs.AgentInfo.ClientConfig.Namespace()
	if err != nil {
		setupLog.Error(err, "failed to get namespace from upstream cluster")
		return err
	}

	fleetRESTConfig, err := cs.AgentInfo.ClientConfig.ClientConfig()
	if err != nil {
		setupLog.Error(err, "failed to get kubeconfig from upstream cluster")
		return err
	}

	fleetMapper, err := mapper.New(fleetRESTConfig)
	if err != nil {
		setupLog.Error(err, "failed to get mappers")
		return err
	}

	fleetSharedFactory, err := newSharedControllerFactory(fleetRESTConfig, fleetMapper, fleetNamespace)
	if err != nil {
		setupLog.Error(err, "failed to build shared controller factory")
		return err
	}

	fleetFactory, err := fleet.NewFactoryFromConfigWithOptions(fleetRESTConfig, &fleet.FactoryOptions{
		SharedControllerFactory: fleetSharedFactory,
	})
	if err != nil {
		setupLog.Error(err, "failed to build fleet factory")
		return err
	}

	setupLog.Info("Starting cluster status ticker", "checkin interval", checkinInterval.String(), "cluster namespace", cs.AgentInfo.ClusterNamespace, "cluster name", cs.AgentInfo.ClusterName)

	go func() {
		clusterstatus.Ticker(ctx,
			cs.Namespace,
			cs.AgentInfo.ClusterNamespace,
			cs.AgentInfo.ClusterName,
			checkinInterval,
			fleetFactory.Fleet().V1alpha1().Cluster(),
		)

		<-ctx.Done()
	}()

	return nil
}

func newSharedControllerFactory(config *rest.Config, mapper meta.RESTMapper, namespace string) (controller.SharedControllerFactory, error) {
	cf, err := client.NewSharedClientFactory(config, &client.SharedClientFactoryOptions{
		Mapper: mapper,
	})
	if err != nil {
		return nil, err
	}

	cacheFactory := cache2.NewSharedCachedFactory(cf, &cache2.SharedCacheFactoryOptions{
		DefaultNamespace: namespace,
		DefaultResync:    durations.DefaultResyncAgent,
	})
	slowRateLimiter := workqueue.NewTypedItemExponentialFailureRateLimiter[any](
		durations.SlowFailureRateLimiterBase,
		durations.SlowFailureRateLimiterMax,
	)

	return controller.NewSharedControllerFactory(cacheFactory, &controller.SharedControllerFactoryOptions{
		KindRateLimiter: map[schema.GroupVersionKind]workqueue.RateLimiter{
			v1alpha1.SchemeGroupVersion.WithKind("BundleDeployment"): slowRateLimiter,
		},
	}), nil
}
