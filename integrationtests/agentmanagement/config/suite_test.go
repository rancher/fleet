package config_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/integrationtests/utils"
	agentconfig "github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/config"
	"github.com/rancher/fleet/internal/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const systemNamespace = "cattle-fleet-system"

var (
	cfg       *rest.Config
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
	k8sClient client.Client
)

func TestController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "AgentManagement Config Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.Background())
	testEnv = utils.NewEnvTest("../../..")

	var err error
	cfg, err = utils.StartTestEnv(testEnv)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = utils.NewClient(cfg)
	Expect(err).NotTo(HaveOccurred())

	// Initialize global config to prevent config.Get() panics during test setup.
	config.Set(config.DefaultConfig())

	// Create system namespace before starting the manager
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: systemNamespace},
	}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())

	mgr, err := utils.NewManager(cfg)
	Expect(err).NotTo(HaveOccurred())

	err = (&agentconfig.ConfigReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		SystemNamespace: systemNamespace,
	}).SetupWithManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred())
	}()
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).ToNot(HaveOccurred())
})
