package benchmarks_test

import (
	"context"
	"os"
	"path"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gm "github.com/onsi/gomega/gmeasure"

	"github.com/rancher/fleet/benchmarks/record"
	"github.com/rancher/fleet/benchmarks/report"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// GroupLabel is used on bundles. One cannot
	// use v1alpha1.RepoLabel because fleet 0.9 deletes bundles with an
	// invalid repo label. However, bundle labels are propagated to
	// bundledeployments.
	GroupLabel = "fleet.cattle.io/benchmark-group"

	// BenchmarkLabel is set to "true" on clusters that should be included
	// in the benchmark.
	BenchmarkLabel = "fleet.cattle.io/benchmark"
)

var (
	ctx    context.Context
	cancel context.CancelFunc

	k8sClient client.Client
	k         kubectl.Command

	root   = ".."
	scheme = apiruntime.NewScheme()

	// experiments
	name       string
	info       string
	experiment *gm.Experiment

	// cluster registration namespace, contains clusters
	workspace string

	// metrics toggles metrics reporting, old fleet versions don't have
	// metrics
	metrics bool
)

// TestBenchmarkSuite runs the benchmark suite for Fleet.
//
// Inputs for this benchmark suite via env vars:
// * cluster registration namespace, contains clusters
// * timeout for eventually
// * if metrics should be recorded
func TestBenchmarkSuite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Fleet Benchmark Suite")
}

// this will run after BeforeEach, but before the actual experiment
var _ = JustBeforeEach(func() {
	experiment = gm.NewExperiment(name)
	AddReportEntry(experiment.Name, experiment, ReportEntryVisibilityNever)
	experiment.RecordNote(record.Header("Info")+info, gm.Style("{{green}}"))
	record.MemoryUsage(experiment, "MemBefore")
	record.ResourceCount(ctx, experiment, "ResourceCountBefore")
	if metrics {
		record.Metrics(experiment, "Before")
	}
})

// this will run after DeferClean, so clean up is not included in the measurements
var _ = AfterEach(func() {
	record.MemoryUsage(experiment, "MemAfter")
	record.ResourceCount(ctx, experiment, "ResourceCountAfter")
	if metrics {
		record.Metrics(experiment, "After")
	}
})

var _ = BeforeSuite(func() {
	metrics = os.Getenv("FLEET_BENCH_METRICS") == "true"

	tm := os.Getenv("FLEET_BENCH_TIMEOUT")
	if tm == "" {
		tm = "2m"
	}
	dur, err := time.ParseDuration(tm)
	Expect(err).NotTo(HaveOccurred(), "failed to parse timeout duration: "+tm)
	SetDefaultEventuallyTimeout(dur)
	SetDefaultEventuallyPollingInterval(1 * time.Second)

	ctx, cancel = context.WithCancel(context.TODO())

	workspace = os.Getenv("FLEET_BENCH_NAMESPACE")
	if workspace == "" {
		workspace = "fleet-local"
	}

	// client for assets
	k = kubectl.New("", workspace)

	// client for assertions
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(apiextv1.AddToScheme(scheme))

	cfg := ctrl.GetConfigOrDie()

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme, Cache: nil})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	atLeastOneCluster()

	record.Setup(workspace, k8sClient, k)

	// describe the environment this suite is running against
	e := gm.NewExperiment("beforeSetup")
	record.MemoryUsage(e, "MemBefore")
	record.ResourceCount(ctx, e, "ResourceCountBefore")
	record.CRDCount(ctx, e, "CRDCount")
	record.Nodes(ctx, e)
	record.Clusters(ctx, e)
	if metrics {
		record.Metrics(e, "")
	}

	version, err := k.Run("version")
	Expect(err).NotTo(HaveOccurred())
	e.RecordNote(record.Header("Kubernetes Version") + version)
	AddReportEntry("setup", e, ReportEntryVisibilityNever)
})

var _ = AfterSuite(func() {
	e := gm.NewExperiment("afterSetup")
	record.MemoryUsage(e, "MemAfter")
	record.ResourceCount(ctx, e, "ResourceCountAfter")
	AddReportEntry("setup", e, ReportEntryVisibilityNever)

	cancel()
})

var _ = ReportAfterSuite("Summary", func(r Report) {
	if summary, ok := report.New(r); ok {
		AddReportEntry("summary", summary)
	}
})

// atLeastOneCluster validates that the workspace has at least one cluster.
func atLeastOneCluster() {
	GinkgoHelper()

	list := &v1alpha1.ClusterList{}
	err := k8sClient.List(ctx, list, client.InNamespace(workspace), client.MatchingLabels{BenchmarkLabel: "true"})
	Expect(err).ToNot(HaveOccurred(), "failed to list clusters")
	Expect(len(list.Items)).To(BeNumerically(">=", 1))
}

// assetPath returns the path to an asset
func assetPath(p ...string) string {
	parts := append([]string{root, "benchmarks", "assets"}, p...)
	return path.Join(parts...)
}
