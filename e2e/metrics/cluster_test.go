package metrics_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	dto "github.com/prometheus/client_model/go"

	"github.com/rancher/fleet/e2e/testenv"
	"github.com/rancher/fleet/pkg/sharding"
)

type MetricsSelector map[string]map[string][]string

var (
	expectedMetrics = MetricsSelector{
		"fleet_cluster_desired_ready_git_repos":      {},
		"fleet_cluster_ready_git_repos":              {},
		"fleet_cluster_resources_count_desiredready": {},
		"fleet_cluster_resources_count_missing":      {},
		"fleet_cluster_resources_count_modified":     {},
		"fleet_cluster_resources_count_notready":     {},
		"fleet_cluster_resources_count_orphaned":     {},
		"fleet_cluster_resources_count_ready":        {},
		"fleet_cluster_resources_count_unknown":      {},
		"fleet_cluster_resources_count_waitapplied":  {},
		// Expects three metrics with name `fleet_cluster_state` and each of
		// these metrics is expected to have a "state" label with values
		// "NotReady", "Ready" and "WaitCheckIn".
		"fleet_cluster_state": {
			"state": []string{"NotReady", "Ready", "WaitCheckIn"},
		},
	}
)

func eventuallyExpectMetrics(
	name string,
	expectedMetrics MetricsSelector,
	expectExist bool,
) {
	Eventually(func() error {
		return expectMetrics(name, expectedMetrics, expectExist)
	}).ShouldNot(HaveOccurred())
}

func expectMetrics(name string, expectedMetrics MetricsSelector, expectExist bool) error {
	expectToFindOneMetric := func(metrics map[string]*dto.MetricFamily, expectedMetric, labelName, labelValue string) error {
		labels := map[string]string{
			"namespace": "fleet-local",
			"name":      name,
		}
		if labelName != "" && labelValue != "" {
			labels[labelName] = labelValue
		}
		metric, err := et.FindOneMetric(metrics, expectedMetric, labels)

		if expectExist && err != nil {
			return err
		} else if !expectExist && err == nil {
			return fmt.Errorf("metric found but not expected: %v", metric)
		}
		return nil
	}

	metrics, err := et.Get()
	if err != nil {
		return err
	}
	for expectedMetric, expectedLabels := range expectedMetrics {
		if len(expectedLabels) > 0 {
			for label, values := range expectedLabels {
				for _, value := range values {
					err := expectToFindOneMetric(metrics, expectedMetric, label, value)
					if err != nil {
						return err
					}
				}
			}
		} else {
			err := expectToFindOneMetric(metrics, expectedMetric, "", "")
			if err != nil {
				return err
			}
		}
	}
	return nil
}

var _ = Describe("Cluster Metrics", Label("cluster"), func() {
	It("should have metrics for the local cluster resource", func() {
		if shard != "" {
			Skip("local cluster isn't handled by shards and hence cannot be tested using sharding")
		}
		eventuallyExpectMetrics("local", expectedMetrics, true)
	})

	When("the initial cluster object has changed in any way", func() {
		It(
			"should not have duplicated metrics after a cluster has been changed",
			Label("modified"),
			func() {
				name := "testing-modification"
				ns := "fleet-local"
				kw := k.Namespace(ns)

				labels := map[string]string{
					"management.cattle.io/cluster-display-name": "testing",
					"name": "testing",
				}
				if shard != "" {
					labels[sharding.ShardingRefLabel] = shard
				}

				err := testenv.CreateCluster(
					k,
					ns,
					name,
					labels,
					map[string]string{
						"clientID": "testing",
					},
				)
				Expect(err).ToNot(HaveOccurred())

				Eventually(func() (string, error) {
					return kw.Get("clusters.fleet.cattle.io", name)
				}).Should(ContainSubstring(name))

				eventuallyExpectMetrics(name, expectedMetrics, true)

				Expect(kw.Patch(
					"cluster", name,
					"--type=json",
					"-p", `[
						{
							"op": "replace",
							"path": "/spec/clientID",
							"value": "changed"
						}
					]`,
				)).To(ContainSubstring(name))

				eventuallyExpectMetrics(name, expectedMetrics, true)

				DeferCleanup(func() {
					_, _ = kw.Delete("cluster", name)
				})
			},
		)

		It(
			"should not have metrics for a deleted cluster",
			Label("deleted"),
			func() {
				name := "testing-deletion"
				ns := "fleet-local"
				kw := k.Namespace(ns)
				labels := map[string]string{
					"management.cattle.io/cluster-display-name": "testing",
					"name": "testing",
				}
				if shard != "" {
					labels[sharding.ShardingRefLabel] = shard
				}

				Expect(testenv.CreateCluster(
					k,
					ns,
					name,
					labels,
					nil,
				)).ToNot(HaveOccurred())

				Eventually(func() (string, error) {
					return kw.Get("clusters.fleet.cattle.io", name)
				}).Should(ContainSubstring(name))

				eventuallyExpectMetrics(name, expectedMetrics, true)

				Eventually(func() (string, error) {
					return kw.Delete("cluster", name)
				}).Should(ContainSubstring(name))

				eventuallyExpectMetrics(name, expectedMetrics, false)

				DeferCleanup(func() {
					_, _ = kw.Delete("cluster", name)
				})
			},
		)
	})
})
