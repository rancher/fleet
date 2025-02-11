package deploy_test

import (
	"context"
	"os"
	"path"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var (
	ctx            context.Context
	cancel         context.CancelFunc
	cfg            *rest.Config
	testEnv        *envtest.Environment
	tmpdir         string
	kubeconfigPath string

	k8sClient client.Client
	namespace string

	scheme = runtime.NewScheme()
)

func TestFleetDeploy(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet CLI Deploy Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.TODO())
	testEnv = utils.NewEnvTest("../../..")

	var err error
	cfg, err = utils.StartTestEnv(testEnv)
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	tmpdir, _ = os.MkdirTemp("", "fleet-")
	kubeconfigPath = path.Join(tmpdir, "kubeconfig")
	err = utils.WriteKubeConfig(cfg, kubeconfigPath)
	Expect(err).NotTo(HaveOccurred())

	// scheme for k8sClient
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
})

var _ = AfterSuite(func() {
	if os.Getenv("SKIP_CLEANUP") == "true" {
		return
	}
	os.RemoveAll(tmpdir)

	cancel()
	_ = testEnv.Stop()
})
