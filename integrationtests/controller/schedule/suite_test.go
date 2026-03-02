package schedule

import (
	"bytes"
	"context"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/reugn/go-quartz/quartz"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/cmd/controller/reconciler"

	"k8s.io/client-go/rest"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	cancel     context.CancelFunc
	cfg        *rest.Config
	ctx        context.Context
	k8sClient  client.Client
	testenv    *envtest.Environment
	logsBuffer bytes.Buffer

	namespace string
)

func TestFleet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet Schedule Suite")
}

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(60 * time.Second)
	SetDefaultEventuallyPollingInterval(1 * time.Second)

	ctx, cancel = context.WithCancel(context.TODO())
	testenv = utils.NewEnvTest("../../..")

	var err error
	cfg, err = utils.StartTestEnv(testenv)
	Expect(err).NotTo(HaveOccurred())

	// Set up log capture
	GinkgoWriter.TeeTo(&logsBuffer)
	ctrl.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	k8sClient, err = utils.NewClient(cfg)
	Expect(err).NotTo(HaveOccurred())

	mgr, err := utils.NewManager(cfg)
	Expect(err).ToNot(HaveOccurred())

	sched, err := quartz.NewStdScheduler()
	Expect(err).ToNot(HaveOccurred(), "failed to create scheduler")

	err = (&reconciler.ScheduleReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Workers:   50,
		Scheduler: sched,
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred(), "failed to set up manager")

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testenv.Stop()).ToNot(HaveOccurred())
})
