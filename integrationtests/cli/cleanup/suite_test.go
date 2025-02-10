package cleanup

import (
	"context"
	"testing"
	"time"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	timeout = 30 * time.Second
)

var (
	cfg     *rest.Config
	testEnv *envtest.Environment
	ctx     context.Context
	cancel  context.CancelFunc

	namespace string
	scheme    = runtime.NewScheme()
	k8sClient client.Client
)

func TestFleetCLICleanUp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet CLI Cleanup Suite")
}

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(timeout)
	ctx, cancel = context.WithCancel(context.TODO())

	testEnv = utils.NewEnvTest("../../..")
	ctx = log.IntoContext(ctx, ctrl.Log)

	var err error
	cfg, err = utils.StartTestEnv(testEnv)
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	// scheme for k8sClient
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// create a namespace
	namespace, err = utils.NewNamespaceName()
	Expect(err).ToNot(HaveOccurred())
	Expect(k8sClient.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	})).ToNot(HaveOccurred())

})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).ToNot(HaveOccurred())
})
