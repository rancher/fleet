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

var _ = Describe("HelmApp Metrics", Label("helmapp"), func() {

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

		err = testenv.CreateHelmApp(
			kw,
			namespace,
			objName,
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

	When("testing HelmApp metrics", func() {
		helmappMetricNames := []string{
			"fleet_helmapp_desired_ready_clusters",
			"fleet_helmapp_ready_clusters",
			"fleet_helmapp_resources_desired_ready",
			"fleet_helmapp_resources_missing",
			"fleet_helmapp_resources_modified",
			"fleet_helmapp_resources_not_ready",
			"fleet_helmapp_resources_orphaned",
			"fleet_helmapp_resources_ready",
			"fleet_helmapp_resources_unknown",
			"fleet_helmapp_resources_wait_applied",
		}

		It("should have exactly one metric of each type for the helmapp", func() {
			Eventually(func() error {
				metrics, err := etHelmApp.Get()
				Expect(err).ToNot(HaveOccurred())
				for _, metricName := range helmappMetricNames {
					metric, err := etHelmApp.FindOneMetric(
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

		When("the HelmApp is changed", func() {
			It("it should not duplicate metrics", Label("oci-registry"), func() {
				ociRef, err := zothelper.GetOCIReference(k)
				Expect(err).ToNot(HaveOccurred(), ociRef)

				chartPath := fmt.Sprintf("%s/sleeper-chart", ociRef)

				out, err := kw.Patch(
					"helmapp", objName,
					"--type=json",
					"-p", fmt.Sprintf(`[{"op": "replace", "path": "/spec/helm/chart", "value": %s}]`, chartPath),
				)
				Expect(err).ToNot(HaveOccurred(), out)
				Expect(out).To(ContainSubstring("helmapp.fleet.cattle.io/metrics patched"))

				// Wait for it to be changed.
				Eventually(func() (string, error) {
					return kw.Get("helmapp", objName, "-o", "jsonpath={.spec.helm.chart}")
				}).Should(Equal(chartPath))

				var metric *metrics.Metric
				// Expect still no metrics to be duplicated.
				Eventually(func() error {
					metrics, err := etHelmApp.Get()
					Expect(err).ToNot(HaveOccurred())
					for _, metricName := range helmappMetricNames {
						metric, err = etHelmApp.FindOneMetric(
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

			It("should not keep metrics if HelmApp is deleted", Label("helmapp-delete"), func() {
				out, err := kw.Delete("helmapp", objName)
				Expect(err).ToNot(HaveOccurred(), out)

				Eventually(func() error {
					metrics, err := etHelmApp.Get()
					Expect(err).ToNot(HaveOccurred())
					for _, metricName := range helmappMetricNames {
						_, err := etHelmApp.FindOneMetric(
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
