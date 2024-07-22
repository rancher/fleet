package controller

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/reugn/go-quartz/quartz"

	"go.uber.org/mock/gomock"

	"github.com/rancher/fleet/internal/cmd/controller/gitops/reconciler"
	ctrlreconciler "github.com/rancher/fleet/internal/cmd/controller/reconciler"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/manifest"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/git/mocks"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	timeout = 30 * time.Second
)

var (
	cfg            *rest.Config
	testEnv        *envtest.Environment
	ctx            context.Context
	cancel         context.CancelFunc
	k8sClient      client.Client
	logsBuffer     bytes.Buffer
	namespace      string
	expectedCommit string
)

func TestGitJobController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet CLI Cleanup Suite")
}

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(timeout)
	ctx, cancel = context.WithCancel(context.TODO())
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "charts", "fleet-crd", "templates", "crds.yaml")},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = v1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	ctlr := gomock.NewController(GinkgoT())

	// redirect logs to a buffer that we can read in the tests
	GinkgoWriter.TeeTo(&logsBuffer)
	ctrl.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	// return whatever commit the test is expecting
	fetcherMock := mocks.NewMockGitFetcher(ctlr)
	fetcherMock.EXPECT().LatestCommit(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(ctx context.Context, gitrepo *v1alpha1.GitRepo, client client.Client) (string, error) {
			return expectedCommit, nil
		},
	)

	sched := quartz.NewStdScheduler()
	Expect(sched).ToNot(BeNil())

	err = (&reconciler.GitJobReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		Image:      "image",
		Scheduler:  sched,
		GitFetcher: fetcherMock,
		Clock:      reconciler.RealClock{},
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

	store := manifest.NewStore(mgr.GetClient())
	builder := target.New(mgr.GetClient())
	err = (&ctrlreconciler.BundleReconciler{
		Client:  mgr.GetClient(),
		Scheme:  mgr.GetScheme(),
		Builder: builder,
		Store:   store,
		Query:   builder,
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred(), "failed to set up manager")

	go func() {
		defer GinkgoRecover()
		defer ctlr.Finish()
		err = mgr.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).ToNot(HaveOccurred())
})
