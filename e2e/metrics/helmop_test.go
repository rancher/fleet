package metrics_test

import (
	"fmt"
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/metrics"
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	"github.com/rancher/fleet/e2e/testenv/zothelper"
)

var _ = Describe("HelmOp Metrics", Label("helmop"), func() {

	var (
		// kw is the kubectl command for namespace the workload is deployed to
		kw        kubectl.Command
		namespace string
		objName   = "metrics"
		version   = "0.1.0"
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		namespace = testenv.NewNamespaceName(
			objName,
			rand.New(rand.NewSource(time.Now().UnixNano())),
		)
		kw = k.Namespace(namespace)

		out, err := k.Create("ns", namespace)
		Expect(err).ToNot(HaveOccurred(), out)

		err = testenv.CreateHelmOp(
			kw,
			namespace,
			objName,
			"",
			"oci://ghcr.io/rancher/fleet-test-configmap-chart",
			version,
			shard,
		)
		Expect(err).ToNot(HaveOccurred())

		DeferCleanup(func() {
			out, err = k.Delete("ns", namespace)
			Expect(err).ToNot(HaveOccurred(), out)
		})
	})

	When("testing HelmOp metrics", func() {
		helmopMetricNames := []string{
			"fleet_helmop_desired_ready_clusters",
			"fleet_helmop_ready_clusters",
			"fleet_helmop_resources_desired_ready",
			"fleet_helmop_resources_missing",
			"fleet_helmop_resources_modified",
			"fleet_helmop_resources_not_ready",
			"fleet_helmop_resources_orphaned",
			"fleet_helmop_resources_ready",
			"fleet_helmop_resources_unknown",
			"fleet_helmop_resources_wait_applied",
		}

		It("should have exactly one metric of each type for the helmop", func() {
			Eventually(func() error {
				metrics, err := etHelmOp.Get()
				Expect(err).ToNot(HaveOccurred())
				for _, metricName := range helmopMetricNames {
					metric, err := etHelmOp.FindOneMetric(
						metrics,
						metricName,
						map[string]string{
							"name":      objName,
							"namespace": namespace,
						},
					)
					if err != nil {
						GinkgoWriter.Printf("ERROR Getting metric: %s: %v\n", metricName, err)
						return err
					}
					Expect(metric.Gauge.GetValue()).To(Equal(float64(0)))
				}
				return nil
			}).ShouldNot(HaveOccurred())
		})

		When("the HelmOp is changed", func() {
			It("it should not duplicate metrics", Label("oci-registry"), func() {
				ociRef, err := zothelper.GetOCIReference(k)
				Expect(err).ToNot(HaveOccurred(), ociRef)

				chartPath := fmt.Sprintf("%s/sleeper-chart", ociRef)

				out, err := kw.Patch(
					"helmop", objName,
					"--type=json",
					"-p", fmt.Sprintf(`[{"op": "replace", "path": "/spec/helm/chart", "value": %s}]`, chartPath),
				)
				Expect(err).ToNot(HaveOccurred(), out)
				Expect(out).To(ContainSubstring("helmop.fleet.cattle.io/metrics patched"))

				// Wait for it to be changed.
				Eventually(func() (string, error) {
					return kw.Get("helmop", objName, "-o", "jsonpath={.spec.helm.chart}")
				}).Should(Equal(chartPath))

				var metric *metrics.Metric
				// Expect still no metrics to be duplicated.
				Eventually(func() error {
					metrics, err := etHelmOp.Get()
					Expect(err).ToNot(HaveOccurred())
					for _, metricName := range helmopMetricNames {
						metric, err = etHelmOp.FindOneMetric(
							metrics,
							metricName,
							map[string]string{
								"name":      objName,
								"namespace": namespace,
							},
						)
						if err != nil {
							return err
						}
						if metric.LabelValue("chart") != chartPath {
							return fmt.Errorf("path for metric %s unchanged", metricName)
						}
					}
					return nil
				}).ShouldNot(HaveOccurred())
			})

			It("should not keep metrics if HelmOp is deleted", Label("helmop-delete"), func() {
				out, err := kw.Delete("helmop", objName)
				Expect(err).ToNot(HaveOccurred(), out)

				Eventually(func() error {
					metrics, err := etHelmOp.Get()
					Expect(err).ToNot(HaveOccurred())
					for _, metricName := range helmopMetricNames {
						_, err := etHelmOp.FindOneMetric(
							metrics,
							metricName,
							map[string]string{
								"name":      objName,
								"namespace": namespace,
							},
						)
						if err == nil {
							return fmt.Errorf("metric %s found", metricName)
						}
					}
					return nil
				}).ShouldNot(HaveOccurred())
			})
		})
	})
})
