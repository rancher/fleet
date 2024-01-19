package gitrepo

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/reugn/go-quartz/quartz"

	"github.com/rancher/fleet/internal/cmd/controller/reconciler"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	gitjob "github.com/rancher/fleet/pkg/apis/gitjob.cattle.io/v1"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/kubectl/pkg/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	cancel    context.CancelFunc
	cfg       *rest.Config
	ctx       context.Context
	testEnv   *envtest.Environment
	k8sClient client.Client

	namespace string
)

const (
	timeout = 30 * time.Second
)

func TestFleet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet GitRepo Suite")
}

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(timeout)

	ctx, cancel = context.WithCancel(context.TODO())
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "charts", "fleet-crd", "templates", "crds.yaml"),
			filepath.Join("..", "..", "..", "charts", "fleet-crd", "templates", "gitjobs-crds.yaml"),
		},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	utilruntime.Must(v1alpha1.AddToScheme(scheme.Scheme))
	utilruntime.Must(gitjob.AddToScheme(scheme.Scheme))
	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{Development: true})))

	config.Set(config.DefaultConfig())

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:         scheme.Scheme,
		LeaderElection: false,
		Metrics:        metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).ToNot(HaveOccurred())

	sched := quartz.NewStdScheduler()
	Expect(sched).ToNot(BeNil())

	// Set up the gitrepo reconciler
	err = (&reconciler.GitRepoReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),

		Scheduler: sched,
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred(), "failed to set up manager")

	sched.Start(ctx)
	DeferCleanup(func() {
		sched.Stop()
	})

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).ToNot(HaveOccurred())
})
