package bundledeployment

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
	RunSpecs(t, "Fleet BundleDeployment Suite")
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

	// Set up the clustergroup reconciler
	err = (&reconciler.BundleDeploymentReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
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

func createBundleDeployment(name, namespace string, options v1alpha1.BundleDeploymentOptions) (*v1alpha1.BundleDeployment, error) {
	bundleDeployment := v1alpha1.BundleDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"foo": "bar"},
		},
		Spec: v1alpha1.BundleDeploymentSpec{
			Options: options,
		},
	}

	return &bundleDeployment, k8sClient.Create(ctx, &bundleDeployment)
}
