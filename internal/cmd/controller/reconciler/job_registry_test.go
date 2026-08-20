package reconciler

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/reugn/go-quartz/quartz"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("jobRegistry", func() {
	var registry *jobRegistry

	BeforeEach(func() {
		registry = &jobRegistry{}
	})

	Context("when the key is nil", func() {
		It("reports a miss on get", func() {
			job, found := registry.get(nil)

			Expect(found).To(BeFalse())
			Expect(job).To(BeNil())
		})

		It("does not store the job", func() {
			registry.store(nil, &CronDurationJob{})

			Expect(registry.list()).To(BeEmpty())
		})

		It("leaves registered jobs untouched on delete", func() {
			key := quartz.NewJobKey("schedule-default/test-schedule")
			job := &CronDurationJob{}
			registry.store(key, job)

			registry.delete(nil)

			stored, found := registry.get(key)
			Expect(found).To(BeTrue())
			Expect(stored).To(BeIdenticalTo(job))
		})
	})

	Context("when looking up clusters", func() {
		BeforeEach(func() {
			registry.store(quartz.NewJobKey("schedule-default/one"), jobTargeting("default", "cluster1"))
			registry.store(quartz.NewJobKey("schedule-default/two"), jobTargeting("default", "cluster1", "cluster2"))
			registry.store(quartz.NewJobKey("schedule-other/three"), jobTargeting("other", "cluster3"))
		})

		It("reports targeted clusters as scheduled", func() {
			Expect(registry.isClusterScheduled("cluster1", "default")).To(BeTrue())
			Expect(registry.isClusterScheduled("cluster2", "default")).To(BeTrue())
			Expect(registry.isClusterScheduled("cluster3", "other")).To(BeTrue())
		})

		It("does not report untargeted clusters as scheduled", func() {
			Expect(registry.isClusterScheduled("cluster4", "default")).To(BeFalse())
		})

		It("does not match a cluster targeted in another namespace", func() {
			Expect(registry.isClusterScheduled("cluster3", "default")).To(BeFalse())
			Expect(registry.isClusterScheduled("cluster1", "other")).To(BeFalse())
		})

		It("returns only clusters which no job targets", func() {
			notScheduled := registry.clustersNotScheduled(
				[]string{"cluster1", "cluster2", "cluster3", "cluster4"},
				"default",
			)

			Expect(notScheduled).To(Equal([]string{"cluster3", "cluster4"}))
		})

		It("returns all clusters as not scheduled when the registry is empty", func() {
			Expect((&jobRegistry{}).clustersNotScheduled([]string{"cluster1"}, "default")).
				To(Equal([]string{"cluster1"}))
		})
	})
})

// jobTargeting builds a job whose schedule lives in the given namespace and which targets the
// given clusters.
func jobTargeting(namespace string, clusters ...string) *CronDurationJob {
	return &CronDurationJob{
		Schedule: &fleet.Schedule{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
		},
		MatchingClusters: clusters,
	}
}
