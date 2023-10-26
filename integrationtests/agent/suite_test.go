package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/cmd/agent/controllers/bundledeployment"
	"github.com/rancher/fleet/internal/cmd/agent/deployer"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/cleanup"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/driftdetect"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/monitor"
	"github.com/rancher/fleet/internal/cmd/agent/trigger"
	"github.com/rancher/fleet/internal/helmdeployer"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	fleetgen "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/lasso/pkg/cache"
	lassoclient "github.com/rancher/lasso/pkg/client"
	"github.com/rancher/lasso/pkg/controller"
	"github.com/rancher/wrangler/v2/pkg/apply"
	"github.com/rancher/wrangler/v2/pkg/generated/controllers/core"
	"github.com/rancher/wrangler/v2/pkg/genericcondition"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var (
	cfg       *rest.Config
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
	k8sClient client.Client
	specEnvs  map[string]*specEnv
)

const (
	assetsPath = "assets"
	timeout    = 30 * time.Second
)

type specResources func() map[string][]v1alpha1.BundleResource

func TestFleet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet Suite")
}

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(timeout)
	ctx, cancel = context.WithCancel(context.TODO())
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "charts", "fleet-crd", "templates", "crds.yaml")},
		ErrorIfCRDPathMissing: true,
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	specEnvs = make(map[string]*specEnv, 2)
	for id, f := range map[string]specResources{"capabilitybundle": capabilityBundleResources, "orphanbundle": orphanBundeResources} {
		namespace, err := utils.NewNamespaceName()
		Expect(err).ToNot(HaveOccurred())
		fmt.Printf("Creating namespace %s\n", namespace)
		Expect(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		})).ToNot(HaveOccurred())

		res := f()
		controller := registerBundleDeploymentController(cfg, namespace, newLookup(res))

		specEnvs[id] = &specEnv{controller: controller, k8sClient: k8sClient, namespace: namespace}
	}
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).ToNot(HaveOccurred())
})

// registerBundleDeploymentController registers a BundleDeploymentController that will watch for changes
// just in the namespace provided. Resources are provided by the lookup parameter.
func registerBundleDeploymentController(cfg *rest.Config, namespace string, lookup *lookup) fleetgen.BundleDeploymentController {
	d, err := discovery.NewDiscoveryClientForConfig(cfg)
	Expect(err).ToNot(HaveOccurred())
	disc := memory.NewMemCacheClient(d)
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(disc)
	dyn, err := dynamic.NewForConfig(cfg)
	Expect(err).ToNot(HaveOccurred())
	trig := trigger.New(ctx, mapper, dyn)
	cf, err := lassoclient.NewSharedClientFactory(cfg, &lassoclient.SharedClientFactoryOptions{
		Mapper: mapper,
	})
	Expect(err).ToNot(HaveOccurred())

	sharedFactory := controller.NewSharedControllerFactory(cache.NewSharedCachedFactory(cf, &cache.SharedCacheFactoryOptions{
		DefaultNamespace: namespace,
		DefaultResync:    durations.DefaultResyncAgent,
	}), nil)

	// this factory will watch BundleDeployments just for the namespace provided
	factory, err := fleet.NewFactoryFromConfigWithOptions(cfg, &fleet.FactoryOptions{
		SharedControllerFactory: sharedFactory,
	})
	Expect(err).ToNot(HaveOccurred())

	restClientGetter := restClientGetter{
		cfg:        cfg,
		discovery:  disc,
		restMapper: mapper,
	}
	coreFactory, err := core.NewFactoryFromConfig(cfg)
	Expect(err).ToNot(HaveOccurred())

	helmDeployer, err := helmdeployer.NewHelm(namespace, namespace, namespace, namespace, &restClientGetter,
		coreFactory.Core().V1().ServiceAccount().Cache(), coreFactory.Core().V1().ConfigMap().Cache(), coreFactory.Core().V1().Secret().Cache())
	Expect(err).ToNot(HaveOccurred())

	wranglerApply, err := apply.NewForConfig(cfg)
	Expect(err).ToNot(HaveOccurred())

	deployManager := deployer.New(
		lookup,
		helmDeployer)
	cleanup := cleanup.New(
		namespace,
		namespace,
		factory.Fleet().V1alpha1().BundleDeployment().Cache(),
		factory.Fleet().V1alpha1().BundleDeployment(),
		helmDeployer)
	monitor := monitor.New(
		namespace,
		namespace,
		namespace,
		helmDeployer,
		wranglerApply)
	driftdetect := driftdetect.New(
		namespace,
		namespace,
		namespace,
		helmDeployer,
		wranglerApply)

	bundledeployment.Register(ctx, trig, mapper, dyn, deployManager, cleanup, monitor, driftdetect, factory.Fleet().V1alpha1().BundleDeployment())

	err = factory.Start(ctx, 50)
	Expect(err).ToNot(HaveOccurred())

	err = coreFactory.Start(ctx, 50)
	Expect(err).ToNot(HaveOccurred())

	return factory.Fleet().V1alpha1().BundleDeployment()
}

// restClientGetter is needed to create the helm deployer. We just need to return the rest.Config for this test.
type restClientGetter struct {
	cfg        *rest.Config
	discovery  discovery.CachedDiscoveryInterface
	restMapper meta.RESTMapper
}

func (c *restClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	panic("should not be reached")
}

func (c *restClientGetter) ToRESTConfig() (*rest.Config, error) {
	return c.cfg, nil
}

func (c *restClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	return c.discovery, nil
}

func (c *restClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	return c.restMapper, nil
}

func newLookup(r map[string][]v1alpha1.BundleResource) *lookup {
	return &lookup{resources: r}
}

type lookup struct {
	resources map[string][]v1alpha1.BundleResource
}

func (l *lookup) Get(id string) (*manifest.Manifest, error) {
	return manifest.New(l.resources[id])
}

type specEnv struct {
	controller fleetgen.BundleDeploymentController
	namespace  string
	k8sClient  client.Client
}

func (se specEnv) isNotReadyAndModified(name string, modifiedStatus v1alpha1.ModifiedStatus, message string) bool {
	bd, err := se.controller.Get(se.namespace, name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	isReadyCondition := checkCondition(bd.Status.Conditions, "Ready", "False", message)

	return cmp.Equal(bd.Status.ModifiedStatus, []v1alpha1.ModifiedStatus{modifiedStatus}) &&
		!bd.Status.NonModified &&
		isReadyCondition
}

func (se specEnv) isBundleDeploymentReadyAndNotModified(name string) bool {
	bd, err := se.controller.Get(se.namespace, name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	return bd.Status.Ready && bd.Status.NonModified
}

func (se specEnv) getService(name string) (corev1.Service, error) {
	nsn := types.NamespacedName{
		Namespace: se.namespace,
		Name:      name,
	}
	svc := corev1.Service{}
	err := se.k8sClient.Get(ctx, nsn, &svc)
	if err != nil {
		return corev1.Service{}, err
	}

	return svc, nil
}

func checkCondition(conditions []genericcondition.GenericCondition, conditionType string, status string, message string) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType && string(condition.Status) == status && strings.Contains(condition.Message, message) {
			return true
		}
	}

	return false
}
