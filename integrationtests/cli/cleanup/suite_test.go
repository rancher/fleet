package cleanup

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rancher/fleet/integrationtests/utils"
	cliclient "github.com/rancher/fleet/internal/client"
	"github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"

	"github.com/rancher/wrangler/v3/pkg/generated/controllers/core"
	"github.com/rancher/wrangler/v3/pkg/generated/controllers/rbac"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	timeout = 30 * time.Second
)

var (
	cfg       *rest.Config
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
	env       *cliclient.Client
	k8sClient client.Client
)

type getter struct {
}

func (g *getter) Get() (*cliclient.Client, error) {
	return env, nil
}

func (g *getter) GetNamespace() string {
	return ""
}

func TestFleetCLICleanUp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet CLI Cleanup Suite")
}

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(timeout)
	ctx, cancel = context.WithCancel(context.TODO())
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "charts", "fleet-crd", "templates", "crds.yaml")},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	namespace, err := utils.NewNamespaceName()
	Expect(err).ToNot(HaveOccurred())
	Expect(k8sClient.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	})).ToNot(HaveOccurred())

	// set up clients
	rbac, err := rbac.NewFactoryFromConfig(cfg)
	Expect(err).ToNot(HaveOccurred())

	fleet, err := fleet.NewFactoryFromConfig(cfg)
	Expect(err).ToNot(HaveOccurred())

	core, err := core.NewFactoryFromConfig(cfg)
	Expect(err).ToNot(HaveOccurred())

	env = &cliclient.Client{
		Namespace: namespace,
		RBAC:      rbac.Rbac().V1(),
		Core:      core.Core().V1(),
		Fleet:     fleet.Fleet().V1alpha1(),
	}
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).ToNot(HaveOccurred())
})
