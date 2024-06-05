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
		kw                kubectl.Command
		namespace         string
		bundleMetricNames = map[string]map[string][]string{
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
	)

	// metricsExist checks that the metrics exist. Custom checks can be
	// added by passing a function to check. This can be used to check for
	// the value of the metrics.
	metricsExist := func(gitRepoName string, check func(metric *metrics.Metric) error) func() error {
		return func() error {
			metrics, err := et.Get()
			Expect(err).ToNot(HaveOccurred())

			identityLabels := map[string]string{
				"name":      gitRepoName,
				"namespace": namespace,
			}

			for metricName, matchLabels := range bundleMetricNames {
				labels := map[string]string{}
				maps.Copy(labels, identityLabels)

				if len(matchLabels) > 0 {
					for labelName, labelValues := range matchLabels {
						for _, labelValue := range labelValues {
							labels[labelName] = labelValue
							metric, err := et.FindOneMetric(metrics, metricName, labels)
							if err != nil {
								return err
							}
							if check != nil {
								if err := check(metric); err != nil {
									return err
								}
							}
						}
					}
				} else {
					metric, err := et.FindOneMetric(metrics, metricName, labels)
					if err != nil {
						return err
					}
					if check != nil {
						if err := check(metric); err != nil {
							return err
						}
					}
				}
			}
			return nil
		}
	}

	// metricsMissing checks that the metrics do not exist.
	metricsMissing := func(gitRepoName string) func() error {
		return func() error {
			metrics, err := et.Get()
			Expect(err).ToNot(HaveOccurred())

			identityLabels := map[string]string{
				"name":      gitRepoName,
				"namespace": namespace,
			}

			for metricName, matchLabels := range bundleMetricNames {
				labels := map[string]string{}
				maps.Copy(labels, identityLabels)

				if len(matchLabels) > 0 {
					for labelName, labelValues := range matchLabels {
						for _, labelValue := range labelValues {
							labels[labelName] = labelValue
							if _, err := et.FindOneMetric(metrics, metricName, labels); err == nil {
								return fmt.Errorf("metric %s found but not expected", metricName)
							}
						}
					}
					return nil
				}

				if _, err := et.FindOneMetric(metrics, metricName, labels); err == nil {
					return fmt.Errorf("metric %s found but not expected", metricName)
				}
			}
			return nil
		}
	}

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		namespace = testenv.NewNamespaceName(
			gitRepoName,
			rand.New(rand.NewSource(time.Now().UnixNano())),
		)
		kw = k.Namespace(namespace)

		out, err := k.Create("ns", namespace)
		Expect(err).ToNot(HaveOccurred(), out)

		// This GitRepo will not create any workload, since it is in a
		// random namespace, which lacks a cluster
		err = testenv.CreateGitRepo(
			kw,
			namespace,
			gitRepoName,
			branch,
			shard,
			"simple-manifest",
		)
		Expect(err).ToNot(HaveOccurred())

		Eventually(metricsExist(gitRepoName+"-simple-manifest", nil)).ShouldNot(HaveOccurred())

		DeferCleanup(func() {
			out, err = k.Delete("ns", namespace)
			Expect(err).ToNot(HaveOccurred(), out)
		})
	})

	When("collecting Bundle metrics", func() {
		It("should have one metric for each specified metric and label value", func() {
			Eventually(metricsExist(gitRepoName+"-simple-manifest", func(metric *metrics.Metric) error {
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
				out, err := kw.Patch(
					"gitrepo", gitRepoName,
					"--type=json",
					"-p", `[{"op": "replace", "path": "/spec/paths", "value": ["simple-chart"]}]`,
				)
				Expect(err).ToNot(HaveOccurred(), out)
				Expect(out).To(ContainSubstring("gitrepo.fleet.cattle.io/metrics patched"))

				Eventually(metricsExist(gitRepoName+"-simple-chart", func(metric *metrics.Metric) error {
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

				Eventually(metricsMissing(gitRepoName)).ShouldNot(HaveOccurred())
			})
		})
	})
})
