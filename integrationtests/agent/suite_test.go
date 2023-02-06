package agent

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/rancher/fleet/modules/agent/pkg/controllers/bundledeployment"
	"github.com/rancher/fleet/modules/agent/pkg/deployer"
	"github.com/rancher/fleet/modules/agent/pkg/trigger"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io"
	fleetgen "github.com/rancher/fleet/pkg/generated/controllers/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/helmdeployer"
	"github.com/rancher/fleet/pkg/manifest"
	"github.com/rancher/wrangler/pkg/apply"
	"github.com/rancher/wrangler/pkg/generated/controllers/core"
	"github.com/rancher/wrangler/pkg/genericcondition"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var (
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
	k8sClient client.Client
)

const (
	assetsPath = "assets"
	timeout    = 5 * time.Second
)

var cfg *rest.Config

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

	customScheme := scheme.Scheme
	customScheme.AddKnownTypes(schema.GroupVersion{Group: "fleet.cattle.io", Version: "v1alpha1"}, &v1alpha1.Bundle{}, &v1alpha1.BundleList{})
	customScheme.AddKnownTypes(schema.GroupVersion{Group: "fleet.cattle.io", Version: "v1alpha1"}, &v1alpha1.BundleDeployment{}, &v1alpha1.BundleDeploymentList{})

	k8sClient, err = client.New(cfg, client.Options{Scheme: customScheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).ToNot(HaveOccurred())
})

func registerBundleDeploymentController(cfg *rest.Config, namespace string, lookup *lookup) fleetgen.BundleDeploymentController {
	d, err := discovery.NewDiscoveryClientForConfig(cfg)
	Expect(err).ToNot(HaveOccurred())

	disc := memory.NewMemCacheClient(d)
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(disc)
	dyn, err := dynamic.NewForConfig(cfg)
	Expect(err).ToNot(HaveOccurred())

	trig := trigger.New(ctx, mapper, dyn)
	factory := fleet.NewFactoryFromConfigOrDie(cfg)
	controller := factory.Fleet().V1alpha1().BundleDeployment()
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

	deployManager := deployer.NewManager(
		namespace,
		namespace,
		namespace,
		namespace,
		controller.Cache(),
		lookup,
		helmDeployer,
		wranglerApply)
	bundledeployment.Register(ctx, trig, mapper, dyn, deployManager, controller)
	err = factory.Start(ctx, 50)
	Expect(err).ToNot(HaveOccurred())

	return controller
}

func newNamespaceName() string {
	return "test-" + strconv.Itoa(int(time.Now().Nanosecond()))
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
	name       string
	k8sClient  client.Client
}

func (se specEnv) isNotReadyAndModified(modifiedStatus v1alpha1.ModifiedStatus, message string) bool {
	bd, err := se.controller.Get(se.namespace, se.name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	isReadyCondition := checkCondition(bd.Status.Conditions, "Ready", "False", message)

	return cmp.Equal(bd.Status.ModifiedStatus, []v1alpha1.ModifiedStatus{modifiedStatus}) &&
		!bd.Status.NonModified &&
		isReadyCondition
}

func (se specEnv) isBundleDeploymentReadyAndNotModified() bool {
	bd, err := se.controller.Get(se.namespace, se.name, metav1.GetOptions{})
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
		if condition.Type == conditionType && string(condition.Status) == status && condition.Message == message {
			return true
		}
	}

	return false
}
