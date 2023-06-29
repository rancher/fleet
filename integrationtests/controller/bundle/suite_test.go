package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/fleetcontroller/controllers/bundle"
	"github.com/rancher/fleet/internal/fleetcontroller/target"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/lasso/pkg/cache"
	lassoclient "github.com/rancher/lasso/pkg/client"
	"github.com/rancher/lasso/pkg/controller"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/generated/controllers/core"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var (
	cfg      *rest.Config
	testEnv  *envtest.Environment
	ctx      context.Context
	cancel   context.CancelFunc
	specEnvs map[string]*specEnv
)

const (
	timeout = 30 * time.Second
)

func TestFleet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet Bundle Suite")
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

	k8sClient, err := client.New(cfg, client.Options{})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	specEnvs = make(map[string]*specEnv, 2)
	for _, id := range []string{"labels", "targets"} {
		namespace, err := utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		fmt.Printf("Creating namespace %s\n", namespace)
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		})).ToNot(HaveOccurred())

		fleet := registerBundleController(cfg, namespace)

		specEnvs[id] = &specEnv{fleet: fleet, k8sClient: k8sClient, namespace: namespace}
	}
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).ToNot(HaveOccurred())
})

func registerBundleController(cfg *rest.Config, namespace string) fleet.Interface {
	d, err := discovery.NewDiscoveryClientForConfig(cfg)
	Expect(err).ToNot(HaveOccurred())
	disc := memory.NewMemCacheClient(d)
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(disc)
	cf, err := lassoclient.NewSharedClientFactory(cfg, &lassoclient.SharedClientFactoryOptions{
		Mapper: mapper,
	})
	Expect(err).ToNot(HaveOccurred())

	sharedFactory := controller.NewSharedControllerFactory(cache.NewSharedCachedFactory(cf, &cache.SharedCacheFactoryOptions{
		DefaultNamespace: namespace,
		DefaultResync:    durations.DefaultResyncAgent,
	}), nil)

	// this factory will watch Bundles just for the namespace provided
	factory, err := fleet.NewFactoryFromConfigWithOptions(cfg, &fleet.FactoryOptions{
		SharedControllerFactory: sharedFactory,
	})
	Expect(err).ToNot(HaveOccurred())

	coreFactory, err := core.NewFactoryFromConfig(cfg)
	Expect(err).ToNot(HaveOccurred())

	wranglerApply, err := apply.NewForConfig(cfg)
	Expect(err).ToNot(HaveOccurred())
	wranglerApply = wranglerApply.WithSetOwnerReference(false, false)

	targetManager := target.New(
		factory.Fleet().V1alpha1().Cluster().Cache(),
		factory.Fleet().V1alpha1().ClusterGroup().Cache(),
		factory.Fleet().V1alpha1().Bundle().Cache(),
		factory.Fleet().V1alpha1().BundleNamespaceMapping().Cache(),
		coreFactory.Core().V1().Namespace().Cache(),
		manifest.NewStore(factory.Fleet().V1alpha1().Content()),
		factory.Fleet().V1alpha1().BundleDeployment().Cache())

	bundle.Register(ctx,
		wranglerApply,
		mapper,
		targetManager,
		factory.Fleet().V1alpha1().Bundle(),
		factory.Fleet().V1alpha1().Cluster(),
		factory.Fleet().V1alpha1().ImageScan(),
		factory.Fleet().V1alpha1().GitRepo().Cache(),
		factory.Fleet().V1alpha1().BundleDeployment())

	err = factory.Start(ctx, 50)
	Expect(err).ToNot(HaveOccurred())

	return factory.Fleet()
}

type specEnv struct {
	fleet     fleet.Interface
	namespace string
	k8sClient client.Client
}
