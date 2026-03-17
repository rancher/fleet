package agentmanagement

import (
	"context"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers"
	agentconfig "github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/kubeconfig"
	"github.com/rancher/wrangler/v3/pkg/leader"
	"github.com/rancher/wrangler/v3/pkg/schemes"

	"github.com/sirupsen/logrus"

	v1 "k8s.io/api/apps/v1"
	policyv1 "k8s.io/api/policy/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var agentScheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(agentScheme))
	utilruntime.Must(fleet.AddToScheme(agentScheme))
}

func start(ctx context.Context, kubeConfig, namespace string, disableBootstrap bool) error {
	clientConfig := kubeconfig.GetNonInteractiveClientConfig(kubeConfig)
	kc, err := clientConfig.ClientConfig()
	if err != nil {
		return err
	}

	// try to claim leadership lease without rate limiting
	localConfig := rest.CopyConfig(kc)
	localConfig.QPS = -1
	localConfig.RateLimiter = nil
	k8s, err := kubernetes.NewForConfig(localConfig)
	if err != nil {
		return err
	}

	// Register all Kinds we apply dynamically so their GVKs resolve
	err = schemes.Register(v1.AddToScheme)
	if err != nil {
		return err
	}
	if err = schemes.Register(policyv1.AddToScheme); err != nil {
		return err
	}
	if err = schemes.Register(schedulingv1.AddToScheme); err != nil {
		return err
	}

	leader.RunOrDie(ctx, namespace, "fleet-agentmanagement-lock", k8s, func(ctx context.Context) {
		// Create controller-runtime manager. Leader election is disabled because
		// wrangler's leader.RunOrDie already holds the lease; the manager starts
		// inside the leader callback.
		mgr, err := ctrl.NewManager(kc, ctrl.Options{
			Scheme:                 agentScheme,
			LeaderElection:         false,
			Metrics:                metricsserver.Options{BindAddress: "0"},
			HealthProbeBindAddress: "",
		})
		if err != nil {
			logrus.Fatal(err)
		}

		if err := (&agentconfig.ConfigReconciler{
			Client:          mgr.GetClient(),
			Scheme:          mgr.GetScheme(),
			SystemNamespace: namespace,
		}).SetupWithManager(mgr); err != nil {
			logrus.Fatal(err)
		}

		go func() {
			if err := mgr.Start(ctx); err != nil {
				logrus.Fatal(err)
			}
		}()

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
