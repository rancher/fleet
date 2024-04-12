package metrics_test

import (
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/e2e/testenv"
)

var (
	expectedMetricsNotExist = map[string]bool{
		"fleet_cluster_desired_ready_git_repos":      false,
		"fleet_cluster_ready_git_repos":              false,
		"fleet_cluster_resources_count_desiredready": false,
		"fleet_cluster_resources_count_missing":      false,
		"fleet_cluster_resources_count_modified":     false,
		"fleet_cluster_resources_count_notready":     false,
		"fleet_cluster_resources_count_orphaned":     false,
		"fleet_cluster_resources_count_ready":        false,
		"fleet_cluster_resources_count_unknown":      false,
		"fleet_cluster_resources_count_waitapplied":  false,
		"fleet_cluster_state":                        false,
	}
)

func eventuallyExpectMetrics(namespace, name string, expectedMetrics map[string]bool) {
	Eventually(func() error {
		return expectMetrics(namespace, name, expectedMetrics)
	}).ShouldNot(HaveOccurred())
}

func expectMetrics(namespace, name string, expectedMetrics map[string]bool) error {
	metrics, err := et.Get()
	if err != nil {
		return err
	}
	for expectedMetric, expectedExist := range expectedMetrics {
		metric, err := et.FindOneMetric(metrics, expectedMetric, map[string]string{
			"namespace": namespace,
			"name":      name,
		})
		if expectedExist && err != nil {
			return err
		} else if !expectedExist && err == nil {
			return fmt.Errorf("metric found but not expected: %v", metric)
		}
	}
	return nil
}

type cluster struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type clusters []cluster

func (cs *clusters) UnmarshalJSON(data []byte) error {
	assertMap := func(i interface{}) map[string]interface{} {
		return i.(map[string]interface{})
	}

	var tmp interface{}
	err := json.Unmarshal(data, &tmp)
	if err != nil {
		return err
	}

	m := tmp.(map[string]interface{})
	items := m["items"].([]interface{})

	*cs = clusters{}

	for _, item := range items {
		metadata := assertMap(assertMap(item)["metadata"])

		c := cluster{}
		c.Namespace = metadata["namespace"].(string)
		c.Name = metadata["name"].(string)

		*cs = append(*cs, c)
	}

	return nil
}

var _ = Describe("Cluster Metrics", Label("cluster"), func() {
	expectedMetricsExist := map[string]bool{
		"fleet_cluster_desired_ready_git_repos":      true,
		"fleet_cluster_ready_git_repos":              true,
		"fleet_cluster_resources_count_desiredready": true,
		"fleet_cluster_resources_count_missing":      true,
		"fleet_cluster_resources_count_modified":     true,
		"fleet_cluster_resources_count_notready":     true,
		"fleet_cluster_resources_count_orphaned":     true,
		"fleet_cluster_resources_count_ready":        true,
		"fleet_cluster_resources_count_unknown":      true,
		"fleet_cluster_resources_count_waitapplied":  true,
		// The value of cluster.Status.Display.State is empty if no issues are
		// found and this means no metric is created.
		"fleet_cluster_state": false,
	}

	It("should have metrics for all existing cluster resources", func() {
		Eventually(func() error {
			clustersOut, err := env.Kubectl.Get(
				"-A", "clusters.fleet.cattle.io",
				"-o", "json",
			)
			Expect(err).ToNot(HaveOccurred())

			var existingClusters clusters
			err = json.Unmarshal([]byte(clustersOut), &existingClusters)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(existingClusters)).ToNot(BeZero())

			Expect(err).ToNot(HaveOccurred())

			for _, cluster := range existingClusters {
				eventuallyExpectMetrics(cluster.Namespace, cluster.Name, expectedMetricsExist)
			}
			return nil
		}).ShouldNot(HaveOccurred())
	},
	)

	When("the initial cluster object has changed in any way", func() {
		It(
			"should not have duplicated metrics after a cluster has been changed",
			Label("modified"),
			func() {
				name := "testing-modification"
				ns := "fleet-local"
				kw := k.Namespace(ns)

				err := testenv.CreateCluster(
					k,
					ns,
					name,
					map[string]string{
						"management.cattle.io/cluster-display-name": "testing",
						"name": "testing",
					},
					map[string]string{
						"clientID": "testing",
					},
				)
				Expect(err).ToNot(HaveOccurred())

				Eventually(func() (string, error) {
					return kw.Get("clusters.fleet.cattle.io", name)
				}).Should(ContainSubstring(name))

				eventuallyExpectMetrics(ns, name, expectedMetricsExist)

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

				eventuallyExpectMetrics(ns, name, expectedMetricsExist)

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

				Expect(testenv.CreateCluster(
					k,
					ns,
					name,
					map[string]string{
						"management.cattle.io/cluster-display-name": "testing",
						"name": "testing",
					},
					nil,
				)).ToNot(HaveOccurred())

				Eventually(func() (string, error) {
					return kw.Get("clusters.fleet.cattle.io", name)
				}).Should(ContainSubstring(name))

				eventuallyExpectMetrics(ns, name, expectedMetricsExist)

				Eventually(func() (string, error) {
					return kw.Delete("cluster", name)
				}).Should(ContainSubstring(name))

				eventuallyExpectMetrics(ns, name, expectedMetricsNotExist)

				DeferCleanup(func() {
					_, _ = kw.Delete("cluster", name)
				})
			},
		)
	})
})
