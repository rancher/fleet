package metrics_test

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/metrics"
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(testenv.FailAndGather)
	RunSpecs(t, "E2E Suite for metrics")
}

var (
	env *testenv.Env
	// k is the kubectl command for the cluster registration namespace
	k         kubectl.Command
	et        metrics.ExporterTest
	etGitjob  metrics.ExporterTest
	etHelmApp metrics.ExporterTest
	shard     string
)

type ServiceData struct {
	Name           string
	Port           int64
	IsDefaultShard bool
	Shard          string
	App            string
}

func getMetricsPort(app string) int64 {
	switch app {
	case "fleet-controller":
		if port := os.Getenv("METRICS_CONTROLLER_PORT"); port != "" {
			i, err := strconv.ParseInt(port, 10, 64)
			Expect(err).ToNot(HaveOccurred())
			return i
		}
	case "gitjob":
		if port := os.Getenv("METRICS_GITJOB_PORT"); port != "" {
			i, err := strconv.ParseInt(port, 10, 64)
			Expect(err).ToNot(HaveOccurred())
			return i
		}
	}
	rs := rand.NewSource(time.Now().UnixNano())
	return rs.Int63()%1000 + 30000
}

// setupLoadBalancer creates a load balancer service for the given app controller.
// If shard is empty, it creates a service for the default (unsharded)
// controller.
// Valid app values are: fleet-controller, gitjob
func setupLoadBalancer(shard string, app string) (metricsURL string) {
	Expect(app).To(Or(Equal("fleet-controller"), Equal("gitjob"), Equal("helmops")))
	rs := rand.NewSource(time.Now().UnixNano())
	port := getMetricsPort(app)
	loadBalancerName := testenv.AddRandomSuffix(app, rs)

	ks := k.Namespace("cattle-fleet-system")
	err := testenv.ApplyTemplate(
		ks,
		testenv.AssetPath("metrics/service.yaml"),
		ServiceData{
			App:            app,
			Name:           loadBalancerName,
			Port:           port,
			IsDefaultShard: shard == "",
			Shard:          shard,
		},
	)
	Expect(err).ToNot(HaveOccurred())

	if ip := os.Getenv("external_ip"); ip != "" {
		metricsURL = fmt.Sprintf("http://%s:%d/metrics", ip, port)
	} else {
		Eventually(func() (string, error) {
			ip, err := ks.Get(
				"service", loadBalancerName,
				"-o", "jsonpath={.status.loadBalancer.ingress[0].ip}",
			)
			metricsURL = fmt.Sprintf("http://%s:%d/metrics", ip, port)
			return ip, err
		}).ShouldNot(BeEmpty())
	}

	DeferCleanup(func() {
		ks := k.Namespace("cattle-fleet-system")
		_, _ = ks.Delete("service", loadBalancerName)
	})

	return metricsURL
}

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(testenv.Timeout)
	SetDefaultEventuallyPollingInterval(time.Second)
	testenv.SetRoot("../..")

	if os.Getenv("SHARD") != "" {
		shard = os.Getenv("SHARD")
	}

	// Enable passing the metrics URL via environment solely for debugging
	// purposes, e.g. when a fleetcontroller is run outside the cluster. This is
	// not intended for regular use.
	var metricsURL string
	if os.Getenv("METRICS_URL") != "" {
		metricsURL = os.Getenv("METRICS_URL")
	} else {
		metricsURL = setupLoadBalancer(shard, "fleet-controller")
	}
	et = metrics.NewExporterTest(metricsURL)

	gitjobMetricsURL := setupLoadBalancer(shard, "gitjob")
	etGitjob = metrics.NewExporterTest(gitjobMetricsURL)

	helmopsMetricsURL := setupLoadBalancer(shard, "helmops")
	etHelmApp = metrics.NewExporterTest(helmopsMetricsURL)

	env = testenv.New()
})
