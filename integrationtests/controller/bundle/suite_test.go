package bundle

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/internal/cmd/controller/reconciler"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"k8s.io/client-go/rest"
	"k8s.io/kubectl/pkg/scheme"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	cancel    context.CancelFunc
	cfg       *rest.Config
	ctx       context.Context
	k8sClient client.Client
	testEnv   *envtest.Environment

	namespace string
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

	utilruntime.Must(v1alpha1.AddToScheme(scheme.Scheme))
	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{Development: true})))

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:         scheme.Scheme,
		LeaderElection: false,
		Metrics:        metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).ToNot(HaveOccurred())

	// Set up the bundle reconciler
	store := manifest.NewStore(mgr.GetClient())
	builder := target.New(mgr.GetClient())

	err = (&reconciler.BundleReconciler{
		Client:  mgr.GetClient(),
		Scheme:  mgr.GetScheme(),
		Builder: builder,
		Store:   store,
		Query:   builder,
	}).SetupWithManager(mgr)
	Expect(err).ToNot(HaveOccurred(), "failed to set up manager")

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).ToNot(HaveOccurred())
})

// createBundle copies all targets from the GitRepo into TargetRestrictions. TargetRestrictions acts as a whitelist to prevent
// the creation of BundleDeployments from Targets created from the TargetCustomizations in the fleet.yaml
// we replicate this behaviour here since this is run in an integration tests that runs just the BundleController.
func createBundle(name, namespace string, targets []v1alpha1.BundleTarget, targetRestrictions []v1alpha1.BundleTarget) (*v1alpha1.Bundle, error) {
	restrictions := []v1alpha1.BundleTargetRestriction{}
	for _, r := range targetRestrictions {
		restrictions = append(restrictions, v1alpha1.BundleTargetRestriction{
			Name:                 r.Name,
			ClusterName:          r.ClusterName,
			ClusterSelector:      r.ClusterSelector,
			ClusterGroup:         r.ClusterGroup,
			ClusterGroupSelector: r.ClusterGroupSelector,
		})
	}
	bundle := v1alpha1.Bundle{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"foo": "bar"},
		},
		Spec: v1alpha1.BundleSpec{
			Targets:            targets,
			TargetRestrictions: restrictions,
		},
	}

	return &bundle, k8sClient.Create(ctx, &bundle)
}

func createCluster(name, controllerNs string, labels map[string]string, clusterNs string) (*v1alpha1.Cluster, error) {
	cluster := &v1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: controllerNs,
			Labels:    labels,
		},
	}
	err := k8sClient.Create(ctx, cluster)
	if err != nil {
		return nil, err
	}
	// Need to set the status.Namespace as it is needed to create a BundleDeployment.
	// Namespace is set by the Cluster controller. We need to do it manually because we are running just the Bundle controller.
	cluster.Status.Namespace = clusterNs
	err = k8sClient.Status().Update(ctx, cluster)
	return cluster, err
}

func createClusterGroup(name, namespace string, selector *metav1.LabelSelector) (*v1alpha1.ClusterGroup, error) {
	cg := &v1alpha1.ClusterGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.ClusterGroupSpec{
			Selector: selector,
		},
	}
	err := k8sClient.Create(ctx, cg)
	return cg, err
}
