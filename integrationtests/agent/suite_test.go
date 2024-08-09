package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/rancher/fleet/internal/cmd/agent/controller"
	"github.com/rancher/fleet/internal/cmd/agent/deployer"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/applied"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/cleanup"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/driftdetect"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/monitor"
	"github.com/rancher/fleet/internal/cmd/agent/trigger"
	"github.com/rancher/fleet/internal/helmdeployer"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"github.com/rancher/wrangler/v3/pkg/genericcondition"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubectl/pkg/scheme"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	cfg       *rest.Config
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
	k8sClient client.Client
)

const (
	clusterNS  = "cluster-test-id"
	assetsPath = "assets"
	timeout    = 30 * time.Second
)

var resources = map[string][]v1alpha1.BundleResource{}

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

	utilruntime.Must(v1alpha1.AddToScheme(scheme.Scheme))
	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	zopts := zap.Options{
		Development: true,
	}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zopts)))

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:         scheme.Scheme,
		LeaderElection: false,
		Metrics:        metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).ToNot(HaveOccurred())

	// Set up the bundledeployment reconciler
	Expect(k8sClient.Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: clusterNS}})).ToNot(HaveOccurred())
	reconciler := newReconciler(ctx, k8sManager, newLookup(resources))
	err = reconciler.SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred(), "failed to set up manager")

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).ToNot(HaveOccurred())
})

// newReconciler creates a new BundleDeploymentReconciler that will watch for changes
// in the test Fleet namespace, using configuration from the provided manager.
// Resources are provided by the lookup parameter.
func newReconciler(ctx context.Context, mgr manager.Manager, lookup *lookup) *controller.BundleDeploymentReconciler {
	upstreamClient := mgr.GetClient()
	// re-use client, since this is a single cluster test
	localClient := upstreamClient

	systemNamespace := "cattle-fleet-system"
	fleetNamespace := clusterNS
	agentScope := ""
	defaultNamespace := "default"

	// Build the helm deployer, which uses a getter for local cluster's client-go client for helm SDK
	d := discovery.NewDiscoveryClientForConfigOrDie(cfg)
	disc := memory.NewMemCacheClient(d)
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(disc)
	getter := &restClientGetter{cfg: cfg, discovery: disc, restMapper: mapper}
	helmDeployer := helmdeployer.New(
		systemNamespace,
		defaultNamespace,
		defaultNamespace,
		agentScope,
	)
	_ = helmDeployer.Setup(ctx, localClient, getter)

	// Build the deployer that the bundledeployment reconciler will use
	deployer := deployer.New(
		localClient,
		mgr.GetAPIReader(),
		lookup,
		helmDeployer,
	)

	// Build the monitor to detect changes
	localDynamic, err := dynamic.NewForConfig(mgr.GetConfig())
	Expect(err).ToNot(HaveOccurred())

	applied, err := applied.NewWithClient(mgr.GetConfig())
	Expect(err).ToNot(HaveOccurred())

	monitor := monitor.New(
		localClient,
		applied,
		helmDeployer,
		defaultNamespace,
		agentScope,
	)

	// Build the drift detector
	trigger := trigger.New(ctx, localDynamic, mgr.GetRESTMapper())
	driftdetect := driftdetect.New(
		trigger,
		upstreamClient,
		mgr.GetAPIReader(),
		applied,
		defaultNamespace,
		defaultNamespace,
		agentScope,
	)

	// Build the clean up
	cleanup := cleanup.New(
		upstreamClient,
		mapper,
		localDynamic,
		helmDeployer,
		fleetNamespace,
		defaultNamespace,
		0,
	)

	return &controller.BundleDeploymentReconciler{
		Client: upstreamClient,

		Scheme:      mgr.GetScheme(),
		LocalClient: localClient,

		Deployer:    deployer,
		Monitor:     monitor,
		DriftDetect: driftdetect,
		Cleanup:     cleanup,

		AgentScope: agentScope,
	}
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

func (l *lookup) Get(_ context.Context, _ client.Reader, id string) (*manifest.Manifest, error) {
	return manifest.New(l.resources[id]), nil
}

type specEnv struct {
	namespace string
}

func (se specEnv) isNotReadyAndModified(name string, modifiedStatus v1alpha1.ModifiedStatus, message string) bool {
	bd := &v1alpha1.BundleDeployment{}
	err := k8sClient.Get(context.TODO(), types.NamespacedName{Namespace: clusterNS, Name: name}, bd, &client.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	isReadyCondition := checkCondition(bd.Status.Conditions, "Ready", "False", message)

	return cmp.Equal(bd.Status.ModifiedStatus, []v1alpha1.ModifiedStatus{modifiedStatus}) &&
		!bd.Status.NonModified &&
		isReadyCondition
}

func (se specEnv) isBundleDeploymentReadyAndNotModified(name string) bool {
	bd := &v1alpha1.BundleDeployment{}
	err := k8sClient.Get(context.TODO(), types.NamespacedName{Namespace: clusterNS, Name: name}, bd, &client.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	return bd.Status.Ready && bd.Status.NonModified
}

func (se specEnv) getService(name string) (corev1.Service, error) {
	nsn := types.NamespacedName{
		Namespace: se.namespace,
		Name:      name,
	}
	svc := corev1.Service{}
	err := k8sClient.Get(ctx, nsn, &svc)
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
