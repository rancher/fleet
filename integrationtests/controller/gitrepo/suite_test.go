package gitrepo

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/reugn/go-quartz/quartz"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/cmd/controller/reconciler"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/config"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	testenv   *envtest.Environment
	k8sClient client.Client

	namespace string
)

func TestFleet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet GitRepo Suite")
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

	// Set up the gitrepo reconciler
	config.Set(config.DefaultConfig())

	sched := quartz.NewStdScheduler()
	Expect(sched).ToNot(BeNil())

	err = (&reconciler.GitRepoReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),

		Scheduler: sched,
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
	Expect(testenv.Stop()).ToNot(HaveOccurred())
})

func createBundle(name, namespace string, targets []v1alpha1.BundleTarget, targetRestrictions []v1alpha1.BundleTarget) (*v1alpha1.Bundle, error) {
	restrictions := []v1alpha1.BundleTargetRestriction{}
	for _, r := range targetRestrictions {
		restrictions = append(restrictions, v1alpha1.BundleTargetRestriction{
			Name:                 r.Name,
			ClusterName:          r.ClusterName,
			ClusterSelector:      r.ClusterSelector,
			ClusterGroup:         r.ClusterGroup,
			ClusterGroupSelector: r.ClusterGroupSelector,
		})
	}
	bundle := v1alpha1.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"foo": "bar"},
		},
		Spec: v1alpha1.BundleSpec{
			Targets:            targets,
			TargetRestrictions: restrictions,
		},
	}

	return &bundle, k8sClient.Create(ctx, &bundle)
}

func createCluster(name, controllerNs string, labels map[string]string, clusterNs string) (*v1alpha1.Cluster, error) {
	cluster := &v1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: controllerNs,
			Labels:    labels,
		},
	}
	err := k8sClient.Create(ctx, cluster)
	if err != nil {
		return nil, err
	}
	cluster.Status.Namespace = clusterNs
	err = k8sClient.Status().Update(ctx, cluster)
	return cluster, err
}
