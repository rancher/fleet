package metrics_test

import (
	"fmt"
	"maps"
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/metrics"
	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

var _ = Describe("Bundle Metrics", Label("bundle"), func() {
	const (
		gitRepoName = "metrics"
		branch      = "master"
	)

	var (
		// kw is the kubectl command for namespace the workload is deployed to
		kw        kubectl.Command
		namespace string
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		namespace = testenv.NewNamespaceName(
			gitRepoName,
			rand.New(rand.NewSource(time.Now().UnixNano())),
		)
		kw = k.Namespace(namespace)

		out, err := k.Create("ns", namespace)
		Expect(err).ToNot(HaveOccurred(), out)

		err = testenv.CreateGitRepo(
			kw,
			namespace,
			gitRepoName,
			branch,
			"simple-manifest",
		)
		Expect(err).ToNot(HaveOccurred())

		DeferCleanup(func() {
			out, err = k.Delete("ns", namespace)
			Expect(err).ToNot(HaveOccurred(), out)
		})
	})

	When("collecting Bundle metrics", func() {
		bundleMetricNames := map[string]map[string][]string{
			"fleet_bundle_desired_ready": {},
			"fleet_bundle_err_applied":   {},
			"fleet_bundle_modified":      {},
			"fleet_bundle_not_ready":     {},
			"fleet_bundle_out_of_sync":   {},
			"fleet_bundle_pending":       {},
			"fleet_bundle_ready":         {},
			"fleet_bundle_wait_applied":  {},
			"fleet_bundle_state": {
				"state": []string{
					string(fleet.Ready),
					string(fleet.NotReady),
					string(fleet.WaitApplied),
					string(fleet.ErrApplied),
					string(fleet.OutOfSync),
					string(fleet.Pending),
					string(fleet.Modified),
				},
			},
		}

		// checkMetrics checks that the metrics exist or not exist. Custom
		// checks can be added by passing a function to check. This can be used
		// to check for the value of the metrics.
		checkMetrics := func(
			gitRepoName string,
			expectExists bool,
			check func(metric *metrics.Metric) error,
		) func() error {
			return func() error {
				et := metrics.NewExporterTest(metricsURL)
				expectOne := func(metricName string, labels map[string]string) error {
					metric, err := et.FindOneMetric(metricName, labels)
					if expectExists && err != nil {
						return err
					} else if !expectExists && err == nil {
						return fmt.Errorf("metric %s found but not expected", metricName)
					}

					if check != nil {
						err = check(metric)
						if err != nil {
							return err
						}
					}
					return nil
				}

				for metricName, matchLabels := range bundleMetricNames {
					identityLabels := map[string]string{
						"name":      gitRepoName,
						"namespace": namespace,
					}
					labels := map[string]string{}
					maps.Copy(labels, identityLabels)

					if len(matchLabels) > 0 {
						for labelName, labelValues := range matchLabels {
							for _, labelValue := range labelValues {
								labels[labelName] = labelValue
								err := expectOne(metricName, labels)
								if err != nil {
									return err
								}
							}
						}
					} else {
						err := expectOne(metricName, labels)
						if err != nil {
							return err
						}
					}
				}
				return nil
			}
		}

		It("should have one metric for each specified metric and label value", func() {
			Eventually(checkMetrics(gitRepoName+"-simple-manifest", true, func(metric *metrics.Metric) error {
				// No cluster exists in the namespace where our GitRepo has been deployed, hence
				// we expect the values of the metrics to be 0.
				if value := metric.Gauge.GetValue(); value != float64(0) {
					return fmt.Errorf("unexpected metric value: expected 0, found %f", value)
				}
				return nil
			})).ShouldNot(HaveOccurred())
		})

		When("the GitRepo (and therefore Bundle) is changed", Label("bundle-modified"), func() {
			It("should not duplicate metrics if Bundle is updated", Label("bundle-update"), func() {
				// et := metrics.NewExporterTest(metricsURL)
				out, err := kw.Patch(
					"gitrepo", gitRepoName,
					"--type=json",
					"-p", `[{"op": "replace", "path": "/spec/paths", "value": ["simple-chart"]}]`,
				)
				Expect(err).ToNot(HaveOccurred(), out)
				Expect(out).To(ContainSubstring("gitrepo.fleet.cattle.io/metrics patched"))

				Eventually(checkMetrics(gitRepoName+"-simple-chart", true, func(metric *metrics.Metric) error {
					if metric.LabelValue("paths") == "simple-manifest" {
						return fmt.Errorf("path for metric %s unchanged", metric.Metric.String())
					}
					return nil
				})).ShouldNot(HaveOccurred())
			})

			It("should not keep metrics if Bundle is deleted", Label("bundle-delete"), func() {
				gitRepoName := gitRepoName + "-simple-manifest"

				var (
					out string
					err error
				)
				Eventually(func() error {
					out, err = kw.Delete("bundle", gitRepoName)
					return err
				}).ShouldNot(HaveOccurred(), out)

				Eventually(checkMetrics(gitRepoName, false, nil)).ShouldNot(HaveOccurred())
			})
		})
	})
})
