// Package agentmanagement_test contains behavioral (envtest) integration tests
// for the agentmanagement controllers. They exercise the controllers against a
// real API server and assert on the resulting cluster state.
//
// # Running
//
//	KUBEBUILDER_ASSETS=$(setup-envtest use --use-env -p path 1.34) \
//	  ginkgo ./integrationtests/agentmanagement/...
//
// # Coverage
//
//	go test -coverprofile=cover.out \
//	  -coverpkg=github.com/rancher/fleet/internal/cmd/controller/agentmanagement/... \
//	  github.com/rancher/fleet/integrationtests/agentmanagement
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

// systemNamespace is the Fleet controller namespace used across all specs.
const systemNamespace = "cattle-fleet-system"

func TestFleet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet AgentManagement Suite")
}

var _ = BeforeSuite(func() {
	utils.SuppressLogs()
	ctx, cancel = context.WithCancel(context.TODO())

	// CRDs are two levels above this package:
	//   integrationtests/agentmanagement/ → ../.. → repo root
	testenv = utils.NewEnvTest("../..")

	var err error
	cfg, err = utils.StartTestEnv(testenv)
	Expect(err).NotTo(HaveOccurred())

	k8sClient, err = utils.NewClient(cfg)
	Expect(err).NotTo(HaveOccurred())

	// Register additional types that manageagent apply-objects need
	// (mirrors what production start.go registers via schemes.Register).
	Expect(schemes.Register(appsv1.AddToScheme)).To(Succeed())
	Expect(schemes.Register(policyv1.AddToScheme)).To(Succeed())
	Expect(schemes.Register(schedulingv1.AddToScheme)).To(Succeed())

	// Seed global config before any controller fires so config.Get() never panics.
	Expect(config.SetAndTrigger(config.DefaultConfig())).To(Succeed())

	// Build a clientcmd.ClientConfig wrapping the envtest *rest.Config so
	// controllers.NewAppContext (which calls cfg.ClientConfig() internally)
	// can use it. utils.FromEnvTestConfig serialises the cert material into
	// a valid kubeconfig; no production seam is needed.
	kubeconfigBytes := utils.FromEnvTestConfig(cfg)
	clientCfg, err := clientcmd.NewClientConfigFromBytes(kubeconfigBytes)
	Expect(err).NotTo(HaveOccurred())

	appCtx, err := controllers.NewAppContext(clientCfg)
	Expect(err).NotTo(HaveOccurred())

	// Register and start the agentmanagement controllers, with bootstrap
	// disabled so the suite does not require local-cluster wiring.
	err = controllers.Register(ctx, appCtx, systemNamespace,
		true, /* disableBootstrap */
		true /* enforceTTL */)
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testenv.Stop()).ToNot(HaveOccurred())
})
