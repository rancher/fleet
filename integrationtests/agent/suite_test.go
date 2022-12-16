package agent

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/modules/agent/pkg/controllers/bundledeployment"
	"github.com/rancher/fleet/modules/agent/pkg/deployer"
	"github.com/rancher/fleet/modules/agent/pkg/trigger"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	gen "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/helmdeployer"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/generated/controllers/core"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"os"
	"path/filepath"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"testing"
)

var testEnv *envtest.Environment
var ctx context.Context
var cancel context.CancelFunc
var controller gen.BundleDeploymentController
var k8sClient client.Client

const (
	DeploymentsNamespace = "fleet-integration-tests"
)

func TestFleet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet Suite")
}

var _ = BeforeSuite(func() {
	ctx, cancel = context.WithCancel(context.TODO())

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "charts", "fleet-crd", "templates", "crds.yaml")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	customScheme := scheme.Scheme
	customScheme.AddKnownTypes(schema.GroupVersion{Group: "fleet.cattle.io", Version: "v1alpha1"}, &v1alpha1.Bundle{}, &v1alpha1.BundleList{})
	customScheme.AddKnownTypes(schema.GroupVersion{Group: "fleet.cattle.io", Version: "v1alpha1"}, &v1alpha1.BundleDeployment{}, &v1alpha1.BundleDeploymentList{})

	k8sClient, err = client.New(cfg, client.Options{Scheme: customScheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	Expect(k8sClient.Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: DeploymentsNamespace,
		},
	})).NotTo(HaveOccurred())

	registerBundleDeploymentController(cfg)
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).ToNot(HaveOccurred())
})

func registerBundleDeploymentController(cfg *rest.Config) {
	d, err := discovery.NewDiscoveryClientForConfig(cfg)
	Expect(err).ToNot(HaveOccurred())

	disc := memory.NewMemCacheClient(d)
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(disc)
	dyn, err := dynamic.NewForConfig(cfg)
	Expect(err).ToNot(HaveOccurred())

	trig := trigger.New(ctx, mapper, dyn)
	factory := fleet.NewFactoryFromConfigOrDie(cfg)
	controller = factory.Fleet().V1alpha1().BundleDeployment()
	restClientGetter := restClientGetter{
		cfg:        cfg,
		discovery:  disc,
		restMapper: mapper,
	}
	coreFactory, err := core.NewFactoryFromConfig(cfg)
	Expect(err).ToNot(HaveOccurred())

	helmDeployer, err := helmdeployer.NewHelm(DeploymentsNamespace, DeploymentsNamespace, DeploymentsNamespace, DeploymentsNamespace, &restClientGetter,
		coreFactory.Core().V1().ServiceAccount().Cache(), coreFactory.Core().V1().ConfigMap().Cache(), coreFactory.Core().V1().Secret().Cache())
	wranglerApply, err := apply.NewForConfig(cfg)
	Expect(err).ToNot(HaveOccurred())

	resources, err := createResources()
	Expect(err).ToNot(HaveOccurred())

	deployManager := deployer.NewManager(
		DeploymentsNamespace,
		DeploymentsNamespace,
		DeploymentsNamespace,
		DeploymentsNamespace,
		controller.Cache(),
		&lookup{resources: resources},
		helmDeployer,
		wranglerApply)
	bundledeployment.Register(ctx, trig, mapper, dyn, deployManager, controller)
	err = factory.Start(ctx, 50)
	Expect(err).ToNot(HaveOccurred())
}

func createResources() (map[string][]v1alpha1.BundleResource, error) {
	v1, err := os.ReadFile("assets/deployment-v1.yaml")
	if err != nil {
		return nil, err
	}
	v2, err := os.ReadFile("assets/deployment-v2.yaml")
	if err != nil {
		return nil, err
	}

	return map[string][]v1alpha1.BundleResource{
		"v1": {
			{
				Name:     "deployment-v1.yaml",
				Content:  string(v1),
				Encoding: "",
			},
		}, "v2": {
			{
				Name:     "deployment-v2.yaml",
				Content:  string(v2),
				Encoding: "",
			},
		},
	}, nil
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

type lookup struct {
	resources map[string][]v1alpha1.BundleResource
}

func (l *lookup) Get(id string) (*manifest.Manifest, error) {
	return manifest.New(l.resources[id])
}
