package metrics_test

import (
	"fmt"
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

var _ = Describe("GitOps Metrics", Label("gitops"), func() {
	var (
		kw        kubectl.Command
		namespace string
		name      = "metrics"
		branch    = "master"
	)

	BeforeEach(func() {
		k = env.Kubectl.Namespace(env.Namespace)
		namespace = testenv.NewNamespaceName(
			name,
			rand.New(rand.NewSource(time.Now().UnixNano())),
		)
		kw = k.Namespace(namespace)

		out, err := k.Create("ns", namespace)
		Expect(err).ToNot(HaveOccurred(), out)

		err = testenv.CreateGitRepo(
			kw,
			namespace,
			name,
			branch,
			shard,
			"simple-manifest",
		)
		Expect(err).ToNot(HaveOccurred())

		DeferCleanup(func() {
			out, err = k.Delete("ns", namespace)
			Expect(err).ToNot(HaveOccurred(), out)
		})
	})

	// This test is ordered because we can safely test for metrics that don't exist if we have
	// waited for metrics that do exist to have appeared, in which case we don't need to use
	// `Eventually` to wait for it to time out.
	When("testing counter metrics", Ordered, func() {
		It("should have exactly one metric of each type for the gitrepo", func() {
			gitOpsMetricNamesExist := []string{
				"fleet_gitjobs_created_success_total",
				"fleet_gitrepo_fetch_latest_commit_success_total",
				"fleet_gitjob_duration_seconds_gauge",
				"fleet_gitjob_duration_seconds",
				"fleet_gitrepo_fetch_latest_commit_duration_seconds",
			}

			Eventually(func() error {
				metrics, err := etGitjob.Get()
				if err != nil {
					return err
				}

				labels := map[string]string{
					"name":      name,
					"namespace": namespace,
				}
				for _, metricName := range gitOpsMetricNamesExist {
					_, err := etGitjob.FindOneMetric(
						metrics,
						metricName,
						labels,
					)
					if err != nil {
						return err
					}
				}
				return nil
			}).ShouldNot(HaveOccurred())
		})

		It("should not find any metric that counts errors", func() {
			// We want metrics to be missing when they count errors. If an error curred, the metric
			// would be present.
			gitOpsMetricNamesMissing := []string{
				"gitrepo_fetch_latest_commit_failure_total",
				"gitjobs_created_failure_total",
			}

			metrics, err := etGitjob.Get()
			Expect(err).ToNot(HaveOccurred())

			labels := map[string]string{
				"name":      name,
				"namespace": namespace,
			}
			for _, metricName := range gitOpsMetricNamesMissing {
				_, err := etGitjob.FindOneMetric(
					metrics,
					metricName,
					labels,
				)
				Expect(err).To(HaveOccurred(), fmt.Sprintf(
					"metric %q with labels %v should not exist, but it does",
					metricName,
					labels,
				))
			}
		})
	})
})
