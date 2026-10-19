package agentmanagement_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/wrangler/v3/pkg/schemes"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers"
	"github.com/rancher/fleet/internal/config"

	appsv1 "k8s.io/api/apps/v1"
	policyv1 "k8s.io/api/policy/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var (
	cancel    context.CancelFunc
	cfg       *rest.Config
	ctx       context.Context
	k8sClient client.Client
	testenv   *envtest.Environment
)

const systemNamespace = "cattle-fleet-system"

func TestFleet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet AgentManagement Suite")
}

var _ = BeforeSuite(func() {
	utils.SuppressLogs()
	ctx, cancel = context.WithCancel(context.TODO())
	testenv = utils.NewEnvTest("../..")

	var err error
	cfg, err = utils.StartTestEnv(testenv)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = utils.NewClient(cfg)
	Expect(err).NotTo(HaveOccurred())

	Expect(schemes.Register(appsv1.AddToScheme)).To(Succeed())
	Expect(schemes.Register(policyv1.AddToScheme)).To(Succeed())
	Expect(schemes.Register(schedulingv1.AddToScheme)).To(Succeed())
	Expect(config.SetAndTrigger(config.DefaultConfig())).To(Succeed())

	kubeconfigBytes := utils.FromEnvTestConfig(cfg)
	clientCfg, err := clientcmd.NewClientConfigFromBytes(kubeconfigBytes)
	Expect(err).NotTo(HaveOccurred())

	appCtx, err := controllers.NewAppContext(clientCfg)
	Expect(err).NotTo(HaveOccurred())

	err = controllers.Register(ctx, appCtx, systemNamespace, true)
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testenv.Stop()).ToNot(HaveOccurred())
})
