package agent_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/wrangler/v3/pkg/genericcondition"

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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/rancher/fleet/integrationtests/utils"
	"github.com/rancher/fleet/internal/cmd/agent/controller"
	"github.com/rancher/fleet/internal/cmd/agent/deployer"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/cleanup"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/desiredset"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/driftdetect"
	"github.com/rancher/fleet/internal/cmd/agent/deployer/monitor"
	"github.com/rancher/fleet/internal/cmd/agent/trigger"
	"github.com/rancher/fleet/internal/helmdeployer"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var (
	cfg       *rest.Config
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
	k8sClient client.Client
	dsClient  *desiredset.Client
)

const (
	clusterNS  = "cluster-test-id"
	assetsPath = "assets"
	timeout    = 60 * time.Second
)

var resources = map[string][]v1alpha1.BundleResource{}

func TestFleet(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet Suite")
}

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(timeout)
	SetDefaultEventuallyPollingInterval(1 * time.Second)

	ctx, cancel = context.WithCancel(context.TODO())
	testEnv = utils.NewEnvTest("../..")

	var err error
	cfg, err = utils.StartTestEnv(testEnv)
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	utilruntime.Must(v1alpha1.AddToScheme(scheme.Scheme))
	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:         scheme.Scheme,
		LeaderElection: false,
		Metrics:        metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).ToNot(HaveOccurred())

	setupFakeContents()

	driftChan := make(chan event.TypedGenericEvent[*v1alpha1.BundleDeployment])

	// Set up the bundledeployment reconciler
	Expect(k8sClient.Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: clusterNS}})).ToNot(HaveOccurred())
	reconciler := newReconciler(ctx, k8sManager, newLookup(resources), driftChan)
	err = reconciler.SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred(), "failed to set up manager")

	// Set up the driftdetect reconciler
	driftReconciler := &controller.DriftReconciler{
		Client: k8sManager.GetClient(),
		Scheme: k8sManager.GetScheme(),

		Deployer:    reconciler.Deployer,
		Monitor:     reconciler.Monitor,
		DriftDetect: reconciler.DriftDetect,

		DriftChan: driftChan,
		Workers:   50,
	}
	err = driftReconciler.SetupWithManager(k8sManager)
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
func newReconciler(ctx context.Context, mgr manager.Manager, lookup *lookup, driftChan chan event.TypedGenericEvent[*v1alpha1.BundleDeployment]) *controller.BundleDeploymentReconciler {
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

	dsClient, err = desiredset.New(mgr.GetConfig())
	Expect(err).ToNot(HaveOccurred())

	monitor := monitor.New(
		localClient,
		dsClient,
		helmDeployer,
		defaultNamespace,
		agentScope,
	)

	// Build the drift detector
	trigger := trigger.New(ctx, localDynamic, mgr.GetRESTMapper())
	driftdetect := driftdetect.New(
		trigger,
		dsClient,
		defaultNamespace,
		defaultNamespace,
		agentScope,
		driftChan,
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
		Workers:    50,
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

func (se specEnv) isNotReadyAndModified(g Gomega, name string, modifiedStatus v1alpha1.ModifiedStatus, message string) {
	bd := &v1alpha1.BundleDeployment{}
	err := k8sClient.Get(context.TODO(), types.NamespacedName{Namespace: clusterNS, Name: name}, bd)

	g.Expect(err).ToNot(HaveOccurred())

	checkCondition(g, bd.Status.Conditions, "Ready", "False", message)

	g.Expect(bd.Status.NonModified).To(BeFalse(), "bd.Status.NonModified has unexpected value")
	g.Expect(bd.Status.ModifiedStatus).To(Equal([]v1alpha1.ModifiedStatus{modifiedStatus}))
}

func (se specEnv) isBundleDeploymentReadyAndNotModified(name string) bool {
	bd := &v1alpha1.BundleDeployment{}
	err := k8sClient.Get(context.TODO(), types.NamespacedName{Namespace: clusterNS, Name: name}, bd)
	if err != nil {
		return false
	}

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

func (se specEnv) getConfigMap(name string) (corev1.ConfigMap, error) {
	nsn := types.NamespacedName{
		Namespace: se.namespace,
		Name:      name,
	}
	cm := corev1.ConfigMap{}
	err := k8sClient.Get(ctx, nsn, &cm)
	if err != nil {
		return corev1.ConfigMap{}, err
	}

	return cm, nil
}

func checkCondition(g Gomega, conditions []genericcondition.GenericCondition, conditionType string, status string, message string) {
	var foundCond *genericcondition.GenericCondition

	for _, condition := range conditions {
		if condition.Type == conditionType && string(condition.Status) == status {
			foundCond = &condition
			break
		}
	}

	g.Expect(foundCond).ToNot(
		BeNil(),
		fmt.Sprintf("Condition with type %q and status %q not found in %v", conditionType, status, conditions),
	)

	g.Expect(foundCond.Message).To(ContainSubstring(message))
}

func createNamespace() string {
	namespace, err := utils.NewNamespaceName()
	Expect(err).ToNot(HaveOccurred())

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
	Expect(k8sClient.Create(context.Background(), ns)).ToNot(HaveOccurred())

	return namespace
}

func setupFakeContents() {
	withStatus, _ := os.ReadFile(assetsPath + "/deployment-with-status.yaml")
	withDeployment, _ := os.ReadFile(assetsPath + "/deployment-with-deployment.yaml")
	v1, _ := os.ReadFile(assetsPath + "/deployment-v1.yaml")
	v2, _ := os.ReadFile(assetsPath + "/deployment-v2.yaml")

	resources = map[string][]v1alpha1.BundleResource{
		"with-status": []v1alpha1.BundleResource{
			{
				Name:     "deployment-with-status.yaml",
				Content:  string(withStatus),
				Encoding: "",
			},
		},
		"with-deployment": []v1alpha1.BundleResource{
			{
				Name:     "deployment-with-deployment.yaml",
				Content:  string(withDeployment),
				Encoding: "",
			},
		},
		"BundleDeploymentConfigMap": []v1alpha1.BundleResource{
			{
				Name: "configmap.yaml",
				Content: `apiVersion: v1
kind: ConfigMap
metadata:
  name: cm1
data:
  key: value
`,
				Encoding: "",
			},
		},
		"v1": []v1alpha1.BundleResource{
			{
				Name:     "deployment-v1.yaml",
				Content:  string(v1),
				Encoding: "",
			},
		},
		"v2": []v1alpha1.BundleResource{
			{
				Name:     "deployment-v2.yaml",
				Content:  string(v2),
				Encoding: "",
			},
		},
		"capabilitiesv1": []v1alpha1.BundleResource{
			{
				Content: "apiVersion: v2\nname: config-chart\ndescription: A test chart that verifies its config\ntype: application\nversion: 0.1.0\nappVersion: \"1.16.0\"\nkubeVersion: '>= 1.20.0-0'\n",
				Name:    "config-chart/Chart.yaml",
			},
			{
				Content: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test-simple-chart-config\ndata:\n  test: \"value123\"\n  name: {{ .Values.name }}\n  kubeVersion: {{ .Capabilities.KubeVersion.Version }}\n  apiVersions: {{ join \", \" .Capabilities.APIVersions |  }}\n  helmVersion: {{ .Capabilities.HelmVersion.Version }}\n",
				Name:    "config-chart/templates/configmap.yaml",
			},
			{
				Content: "helm:\n  chart: config-chart\n  values:\n    name: example-value\n",
				Name:    "fleet.yaml",
			},
		},
		"capabilitiesv2": []v1alpha1.BundleResource{
			{
				Content: "apiVersion: v2\nname: config-chart\ndescription: A test chart that verifies its config\ntype: application\nversion: 0.1.0\nappVersion: \"1.16.0\"\nkubeVersion: '>= 920.920.0-0'\n",
				Name:    "config-chart/Chart.yaml",
			},
			{
				Content: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test-simple-chart-config\ndata:\n  test: \"value123\"\n  name: {{ .Values.name }}\n",
				Name:    "config-chart/templates/configmap.yaml",
			},
			{
				Content: "helm:\n  chart: config-chart\n  values:\n    name: example-value\n",
				Name:    "fleet.yaml",
			},
		},
	}
}
