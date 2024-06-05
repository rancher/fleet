package cluster

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/reugn/go-quartz/quartz"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/cmd/controller/reconciler"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"k8s.io/client-go/rest"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	cancel    context.CancelFunc
	cfg       *rest.Config
	ctx       context.Context
	k8sClient client.Client
	testenv   *envtest.Environment

	namespace string
)

func TestFleet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet Cluster Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.TODO())
	testenv = utils.NewEnvTest()

	var err error
	cfg, err = testenv.Start()
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = utils.NewClient(cfg)
	Expect(err).NotTo(HaveOccurred())

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{Development: true})))

	mgr, err := utils.NewManager(cfg)
	Expect(err).ToNot(HaveOccurred())

	// Set up the cluster reconciler
	err = (&reconciler.ClusterReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),

		Query: &FakeQuery{},
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred(), "failed to set up manager")

	store := manifest.NewStore(mgr.GetClient())
	builder := target.New(mgr.GetClient())

	err = (&reconciler.BundleReconciler{
		Client:  mgr.GetClient(),
		Scheme:  mgr.GetScheme(),
		Builder: builder,
		Store:   store,
		Query:   builder,
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred(), "failed to set up manager")

	err = (&reconciler.BundleDeploymentReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred(), "failed to set up manager")

	sched := quartz.NewStdScheduler()
	Expect(sched).ToNot(BeNil())

	err = (&reconciler.GitRepoReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),

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

type FakeQuery struct {
}

// BundlesForCluster returns empty list, so no cleanup is needed
func (q *FakeQuery) BundlesForCluster(context.Context, *v1alpha1.Cluster) ([]*v1alpha1.Bundle, []*v1alpha1.Bundle, error) {
	return nil, nil, nil
}
