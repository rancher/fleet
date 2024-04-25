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
)

var _ = Describe("BundleDeployment Metrics", Label("bundledeployment"), func() {
	const (
		branch    = "master"
		namespace = "fleet-local" // required for this test to create BundleDeployments
	)

	var (
		// kw is the kubectl command for namespace the workload is deployed to
		kw kubectl.Command
		// objName is going to be "randomized" instead of using a dedicated and
		// random namespace, like it is the case for the other tests.
		objName string
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		kw = k.Namespace(namespace)
		objName = testenv.AddRandomSuffix(
			"metrics",
			rand.New(rand.NewSource(time.Now().UnixNano())),
		)

		err := testenv.CreateGitRepo(
			kw,
			namespace,
			objName,
			branch,
			"simple-manifest",
		)
		Expect(err).ToNot(HaveOccurred())

		DeferCleanup(func() {
			out, err := k.Delete("gitrepo", objName)
			Expect(err).ToNot(HaveOccurred(), out)
		})
	})

	When("testing BundleDeployment metrics", func() {
		bundleDeploymentMetricNames := []string{
			"fleet_bundledeployment_state",
		}
		bundleDeploymentMetricStates := []string{
			"ErrApplied",
			"Modified",
			"NotReady",
			"OutOfSync",
			"Pending",
			"Ready",
			"WaitApplied",
		}

		It("should have exactly one metric for the BundleDeployment", func() {
			et := metrics.NewExporterTest(metricsURL)
			Eventually(func() error {
				for _, metricName := range bundleDeploymentMetricNames {
					for _, state := range bundleDeploymentMetricStates {
						_, err := et.FindOneMetric(
							metricName,
							map[string]string{
								"name":              objName + "-simple-manifest",
								"cluster_namespace": namespace,
								"state":             state,
							},
						)
						if err != nil {
							return err
						}
					}
				}
				return nil
			}).ShouldNot(HaveOccurred())
		})

		Context(
			"when the GitRepo (and therefore Bundle) is changed",
			Label("bundle-altered"),
			func() {
				It(
					"it should not duplicate metrics if Bundle is updated",
					Label("bundle-update"),
					func() {
						et := metrics.NewExporterTest(metricsURL)
						out, err := kw.Patch(
							"gitrepo", objName,
							"--type=json",
							"-p",
							`[{"op": "replace", "path": "/spec/paths", "value": ["simple-chart"]}]`,
						)
						Expect(err).ToNot(HaveOccurred(), out)
						Expect(out).To(ContainSubstring(
							fmt.Sprintf("gitrepo.fleet.cattle.io/%s patched", objName)))

						// Wait for it to be changed and fetched.
						Eventually(func() (string, error) {
							return kw.Get("gitrepo", objName, "-o", "jsonpath={.status.commit}")
						}).ShouldNot(BeEmpty())

						// Expect still no metrics to be duplicated.
						Eventually(func() error {
							for _, metricName := range bundleDeploymentMetricNames {
								for _, metricState := range bundleDeploymentMetricStates {
									_, err = et.FindOneMetric(
										metricName,
										map[string]string{
											"name":              objName + "-simple-chart",
											"cluster_namespace": namespace,
											"state":             metricState,
										},
									)
									if err != nil {
										return err
									}
								}
							}
							return nil
						}).ShouldNot(HaveOccurred())
					})

				It("should not keep metrics if Bundle is deleted", Label("bundle-delete"), func() {
					et := metrics.NewExporterTest(metricsURL)

					objName := objName + "-simple-manifest"

					Eventually(func() (string, error) {
						return kw.Get("-A", "bundledeployment")
					}).Should(ContainSubstring(objName))

					var (
						out string
						err error
					)
					out, err = kw.Delete("bundle", objName)
					Expect(err).ToNot(HaveOccurred(), out)

					Eventually(func() error {
						for _, metricName := range bundleDeploymentMetricNames {
							_, err := et.FindOneMetric(
								metricName,
								map[string]string{
									"name":      objName,
									"namespace": namespace,
								},
							)
							if err == nil {
								return fmt.Errorf("metric %s found but not expected", metricName)
							}
						}
						return nil
					}).ShouldNot(HaveOccurred())
				})
			})
	})
})
