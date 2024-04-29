package metrics_test

import (
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/e2e/metrics"
)

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

			et := metrics.NewExporterTest(metricsURL)
			metrics, err := et.Get()
			Expect(err).ToNot(HaveOccurred())

			for _, cluster := range existingClusters {
				for name, expectedExist := range expectedMetricsExist {
					_, err := et.FindOneMetric(
						metrics,
						name,
						map[string]string{
							"name":      cluster.Name,
							"namespace": cluster.Namespace,
						},
					)
					if expectedExist && err != nil {
						return err
					} else if !expectedExist && err == nil {
						return fmt.Errorf(
							"expected metric %s not to exist, but it exists",
							name,
						)
					}
				}
			}
			return nil
		}).ShouldNot(HaveOccurred())
	},
	)
})
