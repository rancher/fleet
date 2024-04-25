package metrics_test

import (
	"maps"
	"math/rand"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/e2e/metrics"
	"github.com/rancher/fleet/e2e/testenv"
)

var _ = Describe("Cluster Metrics", Label("clustergroup"), func() {
	const (
		namespace = "fleet-local"
	)

	var (
		clusterGroupName string
	)

	expectedMetricsExist := map[string]map[string][]string{
		"fleet_cluster_group_bundle_desired_ready":         {},
		"fleet_cluster_group_bundle_ready":                 {},
		"fleet_cluster_group_cluster_count":                {},
		"fleet_cluster_group_non_ready_cluster_count":      {},
		"fleet_cluster_group_resource_count_desired_ready": {},
		"fleet_cluster_group_resource_count_missing":       {},
		"fleet_cluster_group_resource_count_modified":      {},
		"fleet_cluster_group_resource_count_notready":      {},
		"fleet_cluster_group_resource_count_orphaned":      {},
		"fleet_cluster_group_resource_count_ready":         {},
		"fleet_cluster_group_resource_count_unknown":       {},
		"fleet_cluster_group_resource_count_waitapplied":   {},
		"fleet_cluster_group_state": {
			"state": {
				"Ready",
				"NotReady",
			},
		},
	}

	BeforeEach(func() {
		clusterGroupName = testenv.AddRandomSuffix(
			"test-cluster-group",
			rand.NewSource(time.Now().UnixNano()),
		)
		err := testenv.CreateClusterGroup(
			k,
			namespace,
			clusterGroupName,
			map[string]string{
				"name": "local",
			},
		)
		Expect(err).ToNot(HaveOccurred())

		DeferCleanup(func() {
			out, err := k.Delete(
				"clustergroups.fleet.cattle.io",
				clusterGroupName,
				"-n", namespace,
			)
			Expect(out).To(ContainSubstring("deleted"))
			Expect(err).ToNot(HaveOccurred())
		})
	})

	// The cluster group is created without an UID. This UID is added shortly
	// after the creation of the cluster group. This results in the cluster
	// group being modified and, if not properly checked, duplicated metrics.
	// This is why this test does test for duplicated metrics as well, although
	// it does not look like it.
	It("should have all metrics for a single cluster group once", func() {
		Eventually(func() (string, error) {
			return env.Kubectl.Get(
				"-n", namespace,
				"clustergroups.fleet.cattle.io",
				clusterGroupName,
				"-o", "jsonpath=.metadata.name",
			)
		}).ShouldNot(ContainSubstring("not found"))

		et := metrics.NewExporterTest(metricsURL)

		Eventually(func() error {
			identityLabels := map[string]string{
				"name":      clusterGroupName,
				"namespace": namespace,
			}

			for metricName, expectedLabels := range expectedMetricsExist {
				if len(expectedLabels) == 0 {
					_, err := et.FindOneMetric(metricName, identityLabels)
					if err != nil {
						return err
					}
					Expect(err).ToNot(HaveOccurred())
				} else {
					for labelName, labelValues := range expectedLabels {
						for _, labelValue := range labelValues {
							labels := maps.Clone(identityLabels)
							labels[labelName] = labelValue
							_, err := et.FindOneMetric(metricName, labels)
							if err != nil {
								return err
							}
						}
					}
				}
			}
			return nil
		}).ShouldNot(HaveOccurred())
	})
})
