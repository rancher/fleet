package metrics_test

import (
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2E Suite for metrics")
}

var (
	env *testenv.Env
	// k is the kubectl command for the cluster registration namespace
	k                kubectl.Command
	metricsURL       string
	loadBalancerName string
)

func setupLoadBalancer() {
	rs := rand.NewSource(time.Now().UnixNano())
	port := rs.Int63()%1000 + 30000
	loadBalancerName = testenv.AddRandomSuffix("fleetcontroller", rs)

	ks := k.Namespace("cattle-fleet-system")
	err := testenv.ApplyTemplate(
		ks,
		testenv.AssetPath("metrics/fleetcontroller_service.yaml"),
		map[string]interface{}{
			"Name": loadBalancerName,
			"Port": port,
		},
	)
	Expect(err).ToNot(HaveOccurred())

	if os.Getenv("METRICS_URL") != "" {
		metricsURL = os.Getenv("METRICS_URL")
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
}

func tearDownLoadBalancer() {
	ks := k.Namespace("cattle-fleet-system")
	out, err := ks.Delete("service", loadBalancerName)
	Expect(err).ToNot(HaveOccurred(), out)
}

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)
	testenv.SetRoot("../..")

	setupLoadBalancer()

	env = testenv.New()
})

var _ = AfterSuite(func() {
	tearDownLoadBalancer()
})
