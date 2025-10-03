package reconciler

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/reugn/go-quartz/quartz"

	"github.com/rancher/fleet/internal/cmd/controller/target/matcher"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("CronDurationJob", func() {
	var (
		ctx                          context.Context
		k8sclient                    client.Client
		scheduler                    quartz.Scheduler
		schedule                     *fleet.Schedule
		cluster, cluster2            *fleet.Cluster
		clusterGroup1, clusterGroup2 *fleet.ClusterGroup
	)

	BeforeEach(func() {
		ctx = context.Background()
		Expect(fleet.AddToScheme(scheme.Scheme)).To(Succeed())

		schedule = &fleet.Schedule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-schedule",
				Namespace: "default",
			},
			Spec: fleet.ScheduleSpec{
				Schedule: "0 */1 * * * *", // Every minute
				Duration: metav1.Duration{Duration: 30 * time.Second},
				Targets: fleet.ScheduleTargets{
					Clusters: []fleet.ScheduleTarget{
						fleet.ScheduleTarget{
							ClusterSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"env": "test"},
							},
						},
					},
				},
			},
		}

		cluster = &fleet.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "default",
				Labels: map[string]string{
					"env":  "test",
					"cg":   "cluster-group1",
					"type": "cluster",
				},
			},
		}

		cluster2 = &fleet.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-2",
				Namespace: "default",
				Labels: map[string]string{
					"env":  "prod",
					"cg":   "cluster-group2",
					"type": "cluster",
				},
			},
		}

		clusterGroup1 = &fleet.ClusterGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster-group-1",
				Namespace: "default",
				Labels: map[string]string{
					"cglabel": "cluster-group1-label",
					"type":    "cluster-group",
				},
			},
			Spec: fleet.ClusterGroupSpec{
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"cg": "cluster-group1"},
				},
			},
		}

		clusterGroup2 = &fleet.ClusterGroup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster-group-2",
				Namespace: "default",
				Labels: map[string]string{
					"cglabel": "cluster-group2-label",
					"type":    "cluster-group",
				},
			},
			Spec: fleet.ClusterGroupSpec{
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"cg": "cluster-group2"},
				},
			},
		}

		k8sclient = fake.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(schedule, cluster, cluster2, clusterGroup1, clusterGroup2).
			WithStatusSubresource(&fleet.Schedule{}, &fleet.Cluster{}).
			Build()

		var err error
		scheduler, err = quartz.NewStdScheduler()
		Expect(err).NotTo(HaveOccurred())
		scheduler.Start(ctx)
	})

	AfterEach(func() {
		scheduler.Stop()
		_ = scheduler.Clear()
	})

	Context("newCronDurationJob", func() {
		It("should create a new job successfully", func() {
			job, err := newCronDurationJob(ctx, schedule, scheduler, k8sclient)
			Expect(err).NotTo(HaveOccurred())
			Expect(job).NotTo(BeNil())
			Expect(job.MatchingClusters).To(ConsistOf("test-cluster"))
			Expect(job.Description()).To(ContainSubstring("CronDurationJob-"))
		})

		It("should fail with an unknown time zone location", func() {
			schedule.Spec.Location = "Invalid/Location"
			_, err := newCronDurationJob(ctx, schedule, scheduler, k8sclient)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("unknown time zone Invalid/Location"))
		})

		It("should fail with invalid schedule (duration too long)", func() {
			schedule.Spec.Duration = metav1.Duration{Duration: 61 * time.Second}
			_, err := newCronDurationJob(ctx, schedule, scheduler, k8sclient)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("duration is too long"))
		})

		It("should find no matching clusters", func() {
			cluster.Labels = map[string]string{"env": "prod"}
			k8sclient = fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(schedule, cluster).
				Build()

			job, err := newCronDurationJob(ctx, schedule, scheduler, k8sclient)
			Expect(err).NotTo(HaveOccurred())
			Expect(job.MatchingClusters).To(BeEmpty())
		})
	})

	Context("checkScheduleAndDuration", func() {
		It("should pass for valid duration", func() {
			err := checkScheduleAndDuration(schedule, time.Local)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should fail for duration equal to schedule interval", func() {
			schedule.Spec.Duration = metav1.Duration{Duration: 1 * time.Minute}
			err := checkScheduleAndDuration(schedule, time.Local)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("duration is too long"))
		})

		It("should fail for duration longer than schedule interval", func() {
			schedule.Spec.Duration = metav1.Duration{Duration: 2 * time.Minute}
			err := checkScheduleAndDuration(schedule, time.Local)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("duration is too long"))
		})
	})

	Context("Job Execution", func() {
		var job *CronDurationJob

		BeforeEach(func() {
			var err error
			job, err = newCronDurationJob(ctx, schedule, scheduler, k8sclient)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should execute start and stop logic", func() {
			// Manually trigger start
			err := job.executeStart(ctx)
			Expect(err).NotTo(HaveOccurred())

			// Verify cluster status after start
			Eventually(func() bool {
				updatedCluster := &fleet.Cluster{}
				err := k8sclient.Get(ctx, types.NamespacedName{Name: "test-cluster", Namespace: "default"}, updatedCluster)
				Expect(err).NotTo(HaveOccurred())
				return updatedCluster.Status.ActiveSchedule
			}).Should(BeTrue())

			// Verify schedule status after start
			updatedSchedule := &fleet.Schedule{}
			err = k8sclient.Get(ctx, types.NamespacedName{Name: "test-schedule", Namespace: "default"}, updatedSchedule)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedSchedule.Status.Active).To(BeTrue())

			// Manually trigger stop
			err = job.executeStop(ctx)
			Expect(err).NotTo(HaveOccurred())

			// Verify cluster status after stop
			Eventually(func() bool {
				updatedCluster := &fleet.Cluster{}
				err := k8sclient.Get(ctx, types.NamespacedName{Name: "test-cluster", Namespace: "default"}, updatedCluster)
				Expect(err).NotTo(HaveOccurred())
				return !updatedCluster.Status.ActiveSchedule
			}).Should(BeTrue())

			// Verify schedule status after stop
			err = k8sclient.Get(ctx, types.NamespacedName{Name: "test-schedule", Namespace: "default"}, updatedSchedule)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedSchedule.Status.Active).To(BeFalse())
		})

		It("should handle cluster label changes between start executions", func() {
			// Initial state: cluster matches
			Expect(job.MatchingClusters).To(ConsistOf("test-cluster"))

			// Manually trigger start
			err := job.executeStart(ctx)
			Expect(err).NotTo(HaveOccurred())

			// Verify cluster is active
			updatedCluster := &fleet.Cluster{}
			Eventually(func() bool {
				err := k8sclient.Get(ctx, types.NamespacedName{Name: "test-cluster", Namespace: "default"}, updatedCluster)
				Expect(err).NotTo(HaveOccurred())
				return updatedCluster.Status.ActiveSchedule
			}).Should(BeTrue())

			// Change cluster label so it no longer matches
			updatedCluster.Labels = map[string]string{"env": "prod"}
			Expect(k8sclient.Update(ctx, updatedCluster)).To(Succeed())

			// Manually trigger stop
			err = job.executeStop(ctx)
			Expect(err).NotTo(HaveOccurred())

			// Manually trigger start again
			err = job.executeStart(ctx)
			Expect(err).NotTo(HaveOccurred())

			// Verify cluster is no longer scheduled
			Eventually(func() bool {
				err := k8sclient.Get(ctx, types.NamespacedName{Name: "test-cluster", Namespace: "default"}, updatedCluster)
				Expect(err).NotTo(HaveOccurred())
				return !updatedCluster.Status.Scheduled
			}).Should(BeTrue())
		})

		It("should handle new matching clusters that appear before execution", func() {
			// Initial state: only 'test-cluster' matches
			Expect(job.MatchingClusters).To(ConsistOf("test-cluster"))

			// Create a new cluster that does not match yet
			newCluster := &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "new-cluster", Namespace: "default", Labels: map[string]string{"env": "prod"}},
			}
			Expect(k8sclient.Create(ctx, newCluster)).To(Succeed())

			// Update the job's internal list of matching clusters
			job.MatchingClusters, _ = matchingClusters(ctx, job.Matcher, k8sclient, "default")
			Expect(job.MatchingClusters).To(ConsistOf("test-cluster"))

			// Now, change the label of the new cluster so it matches
			newCluster.Labels["env"] = "test"
			Expect(k8sclient.Update(ctx, newCluster)).To(Succeed())

			// Manually trigger start. It should pick up the new cluster.
			err := job.executeStart(ctx)
			Expect(err).NotTo(HaveOccurred())

			// Verify both clusters are now active
			Eventually(func() bool {
				updatedNewCluster := &fleet.Cluster{}
				err := k8sclient.Get(ctx, client.ObjectKeyFromObject(newCluster), updatedNewCluster)
				Expect(err).NotTo(HaveOccurred())
				return updatedNewCluster.Status.ActiveSchedule
			}).Should(BeTrue())
		})
	})

	Context("scheduleJob", func() {
		It("should schedule a job and update status", func() {
			// wait a bit if we are too close to second 59...
			// this is to ensure calculations based on minutes and seconds
			waitIfTimeIsTooCloseTo59()

			job, err := newCronDurationJob(ctx, schedule, scheduler, k8sclient)
			Expect(err).NotTo(HaveOccurred())

			timeBeforeSchedule := time.Now()
			err = job.scheduleJob(ctx)
			Expect(err).NotTo(HaveOccurred())

			// Check if job is scheduled
			scheduledJob, err := scheduler.GetScheduledJob(job.key)
			Expect(err).NotTo(HaveOccurred())
			Expect(scheduledJob).NotTo(BeNil())

			// Check schedule status
			updatedSchedule := &fleet.Schedule{}
			err = k8sclient.Get(ctx, types.NamespacedName{Name: "test-schedule", Namespace: "default"}, updatedSchedule)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedSchedule.Status.Active).To(BeFalse())
			// time should be the next minute and 0 seconds
			Expect(updatedSchedule.Status.NextStartTime.Time.Second()).To(BeZero())
			Expect(updatedSchedule.Status.NextStartTime.Time.Minute()).To(Equal(timeBeforeSchedule.Add(time.Minute).Minute()))
			Expect(updatedSchedule.Status.NextStartTime.Time.Hour()).To(Equal(timeBeforeSchedule.Hour()))
			// Expect(updatedSchedule.Status.NextStartTime.Time).To(BeTemporally("~", timeBeforeSchedule.Add(time.Minute), 2*time.Second))
			Expect(updatedSchedule.Status.MatchingClusters).To(Equal(job.MatchingClusters))
		})
	})

	Context("updateJob", func() {
		It("should update an existing job", func() {
			// wait a bit if we are too close to second 59...
			// this is to ensure calculations based on minutes and seconds
			waitIfTimeIsTooCloseTo59()

			job, err := newCronDurationJob(ctx, schedule, scheduler, k8sclient)
			Expect(err).NotTo(HaveOccurred())

			err = job.scheduleJob(ctx)
			Expect(err).NotTo(HaveOccurred())

			// Modify schedule to force an update
			schedule.Spec.Schedule = "0 */2 * * * *"
			updatedJob, err := newCronDurationJob(ctx, schedule, scheduler, k8sclient)
			Expect(err).NotTo(HaveOccurred())

			timeBeforeUpdate := time.Now()
			err = updatedJob.updateJob(ctx)
			Expect(err).NotTo(HaveOccurred())

			scheduledJob, err := scheduler.GetScheduledJob(job.key)
			Expect(err).NotTo(HaveOccurred())
			cronDurationJob, ok := scheduledJob.JobDetail().Job().(*CronDurationJob)
			Expect(ok).To(BeTrue())
			Expect(cronDurationJob.Schedule).To(Equal(schedule))

			// Check schedule status
			updatedSchedule := &fleet.Schedule{}
			err = k8sclient.Get(ctx, types.NamespacedName{Name: "test-schedule", Namespace: "default"}, updatedSchedule)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedSchedule.Status.Active).To(BeFalse())

			// next fire time should be the next even minute and 0 seconds
			Expect(updatedSchedule.Status.NextStartTime.Time.Second()).To(BeZero())
			if timeBeforeUpdate.Minute()%2 == 0 {
				Expect(updatedSchedule.Status.NextStartTime.Time.Minute()).To(Equal(timeBeforeUpdate.Add(2 * time.Minute).Minute()))
			} else {
				Expect(updatedSchedule.Status.NextStartTime.Time.Minute()).To(Equal(timeBeforeUpdate.Add(1 * time.Minute).Minute()))
			}
			if timeBeforeUpdate.Minute() < 59 {
				Expect(updatedSchedule.Status.NextStartTime.Time.Hour()).To(Equal(timeBeforeUpdate.Hour()))
			} else {
				Expect(updatedSchedule.Status.NextStartTime.Time.Hour()).To(Equal(timeBeforeUpdate.Add(1 * time.Hour).Hour()))
			}
			Expect(updatedSchedule.Status.MatchingClusters).To(Equal(job.MatchingClusters))
		})
	})

	Context("ClusterScheduledMatcher", func() {
		It("should match a job if the cluster is in MatchingClusters", func() {
			job, err := newCronDurationJob(ctx, schedule, scheduler, k8sclient)
			Expect(err).NotTo(HaveOccurred())
			err = job.scheduleJob(ctx)
			Expect(err).NotTo(HaveOccurred())

			matcher := NewClusterScheduledMatcher("default", "test-cluster")
			keys, err := scheduler.GetJobKeys(matcher)
			Expect(err).NotTo(HaveOccurred())
			Expect(keys).To(HaveLen(1))
			Expect(keys[0].String()).To(Equal(job.key.String()))
		})

		It("should not match a job if the cluster is not in MatchingClusters", func() {
			job, err := newCronDurationJob(ctx, schedule, scheduler, k8sclient)
			Expect(err).NotTo(HaveOccurred())
			err = job.scheduleJob(ctx)
			Expect(err).NotTo(HaveOccurred())

			matcher := NewClusterScheduledMatcher("default", "another-cluster")
			keys, err := scheduler.GetJobKeys(matcher)
			Expect(err).NotTo(HaveOccurred())
			Expect(keys).To(BeEmpty())
		})

		It("should not match a job if the namespace is different", func() {
			job, err := newCronDurationJob(ctx, schedule, scheduler, k8sclient)
			Expect(err).NotTo(HaveOccurred())
			err = job.scheduleJob(ctx)
			Expect(err).NotTo(HaveOccurred())

			matcher := NewClusterScheduledMatcher("other-ns", "test-cluster")
			keys, err := scheduler.GetJobKeys(matcher)
			Expect(err).NotTo(HaveOccurred())
			Expect(keys).To(BeEmpty())
		})
	})

	Context("getClusterScheduleKeys", func() {
		It("should return keys for scheduled jobs matching a cluster", func() {
			job, err := newCronDurationJob(ctx, schedule, scheduler, k8sclient)
			Expect(err).NotTo(HaveOccurred())
			err = job.scheduleJob(ctx)
			Expect(err).NotTo(HaveOccurred())

			keys, err := getClusterScheduleKeys(scheduler, "test-cluster", "default")
			Expect(err).NotTo(HaveOccurred())
			Expect(keys).To(HaveLen(1))
			Expect(keys[0].String()).To(Equal(job.key.String()))
		})
	})

	Context("getScheduleJobHash", func() {
		It("should return a consistent hash", func() {
			schedule1 := schedule.DeepCopy()
			schedule2 := schedule.DeepCopy()

			hash1, err := getScheduleJobHash(schedule1)
			Expect(err).NotTo(HaveOccurred())

			hash2, err := getScheduleJobHash(schedule2)
			Expect(err).NotTo(HaveOccurred())

			Expect(hash1).To(Equal(hash2))

			// check that changing any single item in the spec changes the hash
			schedule2.Spec.Duration.Duration = 10 * time.Second
			hash3, err := getScheduleJobHash(schedule2)
			Expect(err).NotTo(HaveOccurred())
			Expect(hash1).NotTo(Equal(hash3))

			schedule2 = schedule.DeepCopy()
			schedule2.Spec.Schedule = "0 */10 * * * *"
			hash4, err := getScheduleJobHash(schedule2)
			Expect(err).NotTo(HaveOccurred())
			Expect(hash1).NotTo(Equal(hash4))

			schedule2 = schedule.DeepCopy()
			schedule2.Spec.Location = "Europe/Paris"
			hash5, err := getScheduleJobHash(schedule2)
			Expect(err).NotTo(HaveOccurred())
			Expect(hash1).NotTo(Equal(hash5))

			schedule2 = schedule.DeepCopy()
			schedule2.Spec.Targets.Clusters[0].ClusterSelector.MatchLabels["env"] = "prod"
			hash6, err := getScheduleJobHash(schedule2)
			Expect(err).NotTo(HaveOccurred())
			Expect(hash1).NotTo(Equal(hash6))

			schedule2 = schedule.DeepCopy()
			schedule2.Spec.Targets.Clusters[0].ClusterName = "test"
			hash7, err := getScheduleJobHash(schedule2)
			Expect(err).NotTo(HaveOccurred())
			Expect(hash1).NotTo(Equal(hash7))

			// check that changing metadata does not change the hash
			schedule2 = schedule.DeepCopy()
			schedule2.Labels = map[string]string{"new": "label"}
			hash8, err := getScheduleJobHash(schedule2)
			Expect(err).NotTo(HaveOccurred())
			Expect(hash1).To(Equal(hash8))

		})
	})

	Context("matchingClusters", func() {
		It("should return matching clusters when schedule uses cluster label selector", func() {
			matcher, err := matcher.NewScheduleMatch(schedule)
			Expect(err).NotTo(HaveOccurred())

			clusters, err := matchingClusters(ctx, matcher, k8sclient, "default")
			Expect(err).NotTo(HaveOccurred())
			Expect(clusters).To(ConsistOf("test-cluster"))
		})

		It("should return matching clusters when schedule uses cluster name", func() {
			schedule.Spec.Targets.Clusters[0].ClusterName = "test-cluster"
			schedule.Spec.Targets.Clusters[0].ClusterSelector = nil
			matcher, err := matcher.NewScheduleMatch(schedule)
			Expect(err).NotTo(HaveOccurred())

			clusters, err := matchingClusters(ctx, matcher, k8sclient, "default")
			Expect(err).NotTo(HaveOccurred())
			Expect(clusters).To(ConsistOf("test-cluster"))
		})

		It("should return matching clusters when schedule uses cluster group name", func() {
			schedule.Spec.Targets.Clusters[0].ClusterSelector = nil
			schedule.Spec.Targets.Clusters[0].ClusterGroup = "cluster-group-1"
			matcher, err := matcher.NewScheduleMatch(schedule)
			Expect(err).NotTo(HaveOccurred())

			clusters, err := matchingClusters(ctx, matcher, k8sclient, "default")
			Expect(err).NotTo(HaveOccurred())
			Expect(clusters).To(ConsistOf("test-cluster"))
		})

		It("should return matching clusters when schedule uses cluster group selector", func() {
			schedule.Spec.Targets.Clusters[0].ClusterSelector = nil
			schedule.Spec.Targets.Clusters[0].ClusterGroupSelector = &metav1.LabelSelector{
				MatchLabels: map[string]string{"cglabel": "cluster-group1-label"},
			}
			matcher, err := matcher.NewScheduleMatch(schedule)
			Expect(err).NotTo(HaveOccurred())

			clusters, err := matchingClusters(ctx, matcher, k8sclient, "default")
			Expect(err).NotTo(HaveOccurred())
			Expect(clusters).To(ConsistOf("test-cluster"))
		})

		It("should return both clusters when schedule uses label selector that matches both", func() {
			schedule.Spec.Targets.Clusters[0].ClusterSelector.MatchLabels = map[string]string{"type": "cluster"}
			matcher, err := matcher.NewScheduleMatch(schedule)
			Expect(err).NotTo(HaveOccurred())

			clusters, err := matchingClusters(ctx, matcher, k8sclient, "default")
			Expect(err).NotTo(HaveOccurred())
			Expect(clusters).To(ConsistOf("test-cluster", "test-cluster-2"))
		})

		It("should return both clusters when schedule uses cluster group selector that matches both", func() {
			schedule.Spec.Targets.Clusters[0].ClusterSelector.MatchLabels = nil
			schedule.Spec.Targets.Clusters[0].ClusterGroupSelector = &metav1.LabelSelector{
				MatchLabels: map[string]string{"type": "cluster-group"},
			}
			matcher, err := matcher.NewScheduleMatch(schedule)
			Expect(err).NotTo(HaveOccurred())

			clusters, err := matchingClusters(ctx, matcher, k8sclient, "default")
			Expect(err).NotTo(HaveOccurred())
			Expect(clusters).To(ConsistOf("test-cluster", "test-cluster-2"))
		})
	})
})

func waitIfTimeIsTooCloseTo59() {
	for {
		now := time.Now()
		// if we are up to second 55... just break
		if now.Second() < 55 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
}
