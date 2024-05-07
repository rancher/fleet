package utils

import (
	"path/filepath"
	"time"

	"github.com/onsi/gomega"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/kubectl/pkg/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
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
func NewEnvTest() *envtest.Environment {
	return &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "charts", "fleet-crd", "templates", "crds.yaml")},
		ErrorIfCRDPathMissing: true,
	}
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
