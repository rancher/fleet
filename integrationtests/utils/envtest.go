package utils

import (
	"os"
	"path/filepath"
	"time"

	"github.com/go-logr/logr"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/kubectl/pkg/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const (
	Timeout         = 30 * time.Second
	PollingInterval = 3 * time.Second
)

func init() {
	gomega.SetDefaultEventuallyTimeout(Timeout)
	gomega.SetDefaultEventuallyPollingInterval(PollingInterval)
}

// NewEnvTest returns a new envtest with the Fleet CRDs loaded.
// Run ginkgo with the -v flag to see the logs in real time.
func NewEnvTest(root string) *envtest.Environment {
	if os.Getenv("CI_SILENCE_CTRL") != "" {
		ctrl.SetLogger(logr.New(log.NullLogSink{}))
	} else {
		ctrl.SetLogger(zap.New(zap.WriteTo(ginkgo.GinkgoWriter), zap.UseDevMode(true)))
	}

	existing := os.Getenv("CI_USE_EXISTING_CLUSTER") == "true"
	return &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join(root, "charts", "fleet-crd", "templates", "crds.yaml")},
		ErrorIfCRDPathMissing: true,
		UseExistingCluster:    &existing,
	}
}

func StartTestEnv(testEnv *envtest.Environment) (*rest.Config, error) {
	cfg, err := testEnv.Start()
	if err != nil {
		return nil, err
	}

	if config := os.Getenv("CI_KUBECONFIG"); config != "" {
		err = WriteKubeConfig(cfg, config)
	}

	return cfg, err
}

// NewClient returns a new controller-runtime client.
func NewClient(cfg *rest.Config) (client.Client, error) {
	utilruntime.Must(v1alpha1.AddToScheme(scheme.Scheme))
	//+kubebuilder:scaffold:scheme

	return client.New(cfg, client.Options{Scheme: scheme.Scheme})
}

// NewManager returns a new controller-runtime manager suitable for testing.
func NewManager(cfg *rest.Config) (ctrl.Manager, error) {
	return ctrl.NewManager(cfg, ctrl.Options{
		Scheme:         scheme.Scheme,
		LeaderElection: false,
		Metrics:        metricsserver.Options{BindAddress: "0"},
	})
}
