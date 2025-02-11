package clustergroup

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/cmd/controller/reconciler"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
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
	RunSpecs(t, "Fleet ClusterGroup Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.TODO())
	testenv = utils.NewEnvTest("../../..")

	var err error
	cfg, err = utils.StartTestEnv(testenv)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = utils.NewClient(cfg)
	Expect(err).NotTo(HaveOccurred())

	mgr, err := utils.NewManager(cfg)
	Expect(err).ToNot(HaveOccurred())

	// Set up the clustergroup reconciler
	err = (&reconciler.ClusterGroupReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred(), "failed to set up manager")

	err = (&reconciler.ClusterReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),

		Query: &FakeQuery{},
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

func createClusterGroup(name, namespace string, selector *metav1.LabelSelector) (*v1alpha1.ClusterGroup, error) {
	cg := &v1alpha1.ClusterGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.ClusterGroupSpec{
			Selector: selector,
		},
	}
	err := k8sClient.Create(ctx, cg)
	return cg, err
}
