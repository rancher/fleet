package controller

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/mock/gomock"

	gitjobv1 "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	"github.com/rancher/gitjob/pkg/controller"
	"github.com/rancher/gitjob/pkg/mocks"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	timeout = 30 * time.Second
)

var (
	cfg       *rest.Config
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
	k8sClient client.Client
)

func TestGitJobController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet CLI Cleanup Suite")
}

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(timeout)
	ctx, cancel = context.WithCancel(context.TODO())
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "chart", "crds", "crds.yaml")},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = gitjobv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	ctlr := gomock.NewController(GinkgoT())
	gitPollerMock := mocks.NewMockGitPoller(ctlr)

	// do nothing if gitPoller is called. gitPoller calls are tested in unit tests
	gitPollerMock.EXPECT().AddOrModifyGitRepoWatch(gomock.Any(), gomock.Any()).AnyTimes()
	gitPollerMock.EXPECT().CleanUpWatches(gomock.Any()).AnyTimes()

	err = (&controller.GitJobReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Image:     "image",
		GitPoller: gitPollerMock,
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred())

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
