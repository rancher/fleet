package reconciler

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/reugn/go-quartz/quartz"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type expected struct {
	scheduledJob         bool
	statusScheduled      bool
	statusActiveSchedule bool
}

var _ = Describe("ScheduleReconciler", func() {
	var (
		ctx        context.Context
		reconciler *ScheduleReconciler
		k8sclient  client.Client
		scheduler  quartz.Scheduler
		schedule   *fleet.Schedule
		cluster    *fleet.Cluster
		req        reconcile.Request
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
						{
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
				Labels:    map[string]string{"env": "test"},
			},
		}

		req = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      "test-schedule",
				Namespace: "default",
			},
		}

		var err error
		scheduler, err = quartz.NewStdScheduler()
		Expect(err).NotTo(HaveOccurred())
		scheduler.Start(ctx)
	})

	JustBeforeEach(func() {
		if k8sclient == nil {
			k8sclient = fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(schedule, cluster).
				WithStatusSubresource(&fleet.Schedule{}, &fleet.Cluster{}).
				Build()
		}

		reconciler = &ScheduleReconciler{
			Client:    k8sclient,
			Scheme:    scheme.Scheme,
			Scheduler: scheduler,
			Recorder:  events.NewFakeRecorder(10),
		}
	})

	AfterEach(func() {
		scheduler.Stop()
		_ = scheduler.Clear()
		k8sclient = nil
	})

	Context("Reconcile", func() {
		It("should add a finalizer, schedule a new job, and update the cluster status", func() {
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Check finalizer
			updatedSchedule := &fleet.Schedule{}
			err = k8sclient.Get(ctx, req.NamespacedName, updatedSchedule)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedSchedule.Finalizers).To(ContainElement(finalize.ScheduleFinalizer))

			// Check job in scheduler
			jobKey := scheduleKey(schedule)
			_, err = scheduler.GetScheduledJob(jobKey)
			Expect(err).NotTo(HaveOccurred())

			// Check cluster status
			updatedCluster := &fleet.Cluster{}
			err = k8sclient.Get(ctx, client.ObjectKeyFromObject(cluster), updatedCluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedCluster.Status.Scheduled).To(BeTrue())
			Expect(updatedCluster.Status.ActiveSchedule).To(BeFalse())
		})

		It("should remove the job and finalizer on schedule deletion", func() {
			// First, reconcile to create the job and add finalizer
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Now, delete the schedule
			err = k8sclient.Get(ctx, req.NamespacedName, schedule)
			Expect(err).NotTo(HaveOccurred())

			err = k8sclient.Delete(ctx, schedule)
			Expect(err).NotTo(HaveOccurred())

			// Reconcile again to handle deletion
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Check job is deleted from scheduler
			jobKey := scheduleKey(schedule)
			_, err = scheduler.GetScheduledJob(jobKey)
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(quartz.ErrJobNotFound))

			// Check finalizer is removed
			err = k8sclient.Get(ctx, req.NamespacedName, schedule)
			Expect(err).To(HaveOccurred()) // Should be gone as finalizer is removed
			Expect(errors.IsNotFound(err)).To(BeTrue())

			// Check cluster status
			updatedCluster := &fleet.Cluster{}
			err = k8sclient.Get(ctx, client.ObjectKeyFromObject(cluster), updatedCluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedCluster.Status.Scheduled).To(BeFalse())
			Expect(updatedCluster.Status.ActiveSchedule).To(BeFalse())
		})

		It("should update the scheduled job when the schedule's spec changes", func() {
			// First reconcile
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Get the first job's description
			jobKey := scheduleKey(schedule)
			scheduledJob, err := scheduler.GetScheduledJob(jobKey)
			Expect(err).NotTo(HaveOccurred())
			originalDescription := scheduledJob.JobDetail().Job().Description()

			// Update schedule spec
			err = k8sclient.Get(ctx, req.NamespacedName, schedule)
			Expect(err).NotTo(HaveOccurred())
			schedule.Spec.Schedule = "0 */2 * * * *" // Change schedule
			Expect(k8sclient.Update(ctx, schedule)).To(Succeed())

			// Reconcile again
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Check job is updated
			updatedScheduledJob, err := scheduler.GetScheduledJob(jobKey)
			Expect(err).NotTo(HaveOccurred())
			newDescription := updatedScheduledJob.JobDetail().Job().Description()
			Expect(newDescription).NotTo(Equal(originalDescription))
		})

		It("should update the cluster's scheduled status when its labels no longer match", func() {
			// Initial reconcile
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Cluster is scheduled
			updatedCluster := &fleet.Cluster{}
			err = k8sclient.Get(ctx, client.ObjectKeyFromObject(cluster), updatedCluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedCluster.Status.Scheduled).To(BeTrue())

			// Change cluster label so it no longer matches
			updatedCluster.Labels = map[string]string{"env": "prod"}
			Expect(k8sclient.Update(ctx, updatedCluster)).To(Succeed())

			// Reconcile schedule again
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Check cluster is no longer scheduled
			err = k8sclient.Get(ctx, client.ObjectKeyFromObject(cluster), updatedCluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedCluster.Status.Scheduled).To(BeFalse())
		})

		It("should set ready condition to false on error", func() {
			// Use an invalid schedule
			schedule.Spec.Schedule = "invalid cron"
			k8sclient = fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(schedule, cluster).
				WithStatusSubresource(&fleet.Schedule{}, &fleet.Cluster{}).
				Build()
			reconciler.Client = k8sclient

			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).ToNot(HaveOccurred())

			updatedSchedule := &fleet.Schedule{}
			err = k8sclient.Get(ctx, req.NamespacedName, updatedSchedule)
			Expect(err).NotTo(HaveOccurred())

			readyCond := condition.Cond(fleet.Ready)
			Expect(readyCond.IsTrue(updatedSchedule)).To(BeFalse())
			Expect(readyCond.GetMessage(updatedSchedule)).To(Equal("parse cron expression: invalid expression length"))
		})
	})

	Context("mapClustersToSchedules", func() {
		It("should enqueue schedules that target a changed cluster", func() {
			// Reconcile to create the job
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Trigger the map function
			requests := reconciler.mapClustersToSchedules(ctx, cluster)
			Expect(requests).To(HaveLen(1))
			Expect(requests[0].NamespacedName).To(Equal(req.NamespacedName))
		})

		It("should not enqueue schedules that do not target a changed cluster", func() {
			// Reconcile to create the job
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Create a cluster that doesn't match
			otherCluster := &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-cluster",
					Namespace: "default",
					Labels:    map[string]string{"env": "prod"},
				},
			}

			// Trigger the map function
			requests := reconciler.mapClustersToSchedules(ctx, otherCluster)
			Expect(requests).To(BeEmpty())
		})
	})

	Context("updateScheduledClusters", func() {
		It("should update the clusters matching in the scheduled jobs and also the Status.Scheduled flag", func() {
			cluster2 := &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster2",
					Namespace: "default",
					Labels:    map[string]string{"env": "test", "foo": "bar"},
				},
			}

			cluster3 := &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster3",
					Namespace: "default",
					Labels:    map[string]string{"env": "test"},
				},
			}

			cluster4 := &fleet.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster4",
					Namespace: "default",
					Labels:    map[string]string{"foo": "bar"},
				},
			}

			k8sclient = fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(schedule, cluster, cluster2, cluster3, cluster4).
				WithStatusSubresource(&fleet.Schedule{}, &fleet.Cluster{}).
				Build()

			reconciler.Client = k8sclient

			// initial reconcile
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// clusters 1, 2 and 3 should be scheduled in the quartz.Scheduler and also
			// be flagged as Scheduled
			checkState(scheduler, k8sclient, "test-cluster", "default",
				expected{scheduledJob: true, statusScheduled: true, statusActiveSchedule: false})
			checkState(scheduler, k8sclient, "test-cluster2", "default",
				expected{scheduledJob: true, statusScheduled: true, statusActiveSchedule: false})
			checkState(scheduler, k8sclient, "test-cluster3", "default",
				expected{scheduledJob: true, statusScheduled: true, statusActiveSchedule: false})
			checkState(scheduler, k8sclient, "test-cluster4", "default",
				expected{scheduledJob: false, statusScheduled: false, statusActiveSchedule: false})

			// force the start of the schedule (so it sets .Status.ActiveSchedule=true)
			jobKey := scheduleKey(schedule)
			job, err := scheduler.GetScheduledJob(jobKey)
			Expect(err).NotTo(HaveOccurred())

			cronDurationJob, ok := job.JobDetail().Job().(*CronDurationJob)
			Expect(ok).To(BeTrue())

			// Manually trigger start
			err = cronDurationJob.executeStart(ctx)
			Expect(err).NotTo(HaveOccurred())

			// check now that the clusters have the expected values, specially Status.ActiveSchedule
			checkState(scheduler, k8sclient, "test-cluster", "default",
				expected{scheduledJob: true, statusScheduled: true, statusActiveSchedule: true})
			checkState(scheduler, k8sclient, "test-cluster2", "default",
				expected{scheduledJob: true, statusScheduled: true, statusActiveSchedule: true})
			checkState(scheduler, k8sclient, "test-cluster3", "default",
				expected{scheduledJob: true, statusScheduled: true, statusActiveSchedule: true})
			checkState(scheduler, k8sclient, "test-cluster4", "default",
				expected{scheduledJob: false, statusScheduled: false, statusActiveSchedule: false})

			// update the schedule, now it only looks for the label foo=bar
			scheduleUpdated := &fleet.Schedule{}
			Expect(k8sclient.Get(ctx, req.NamespacedName, scheduleUpdated)).To(Succeed())

			scheduleUpdated.Spec.Targets.Clusters = []fleet.ScheduleTarget{
				{
					ClusterSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"foo": "bar"},
					},
				},
			}
			Expect(k8sclient.Update(ctx, scheduleUpdated)).To(Succeed())

			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// cluster 2 and 4 should be still targeted.
			checkState(scheduler, k8sclient, "test-cluster", "default",
				expected{scheduledJob: false, statusScheduled: false, statusActiveSchedule: false})
			// cluster 2 had Status.ActiveSchedule set to true, but because we updated the Schedule
			// it should be back to false.
			checkState(scheduler, k8sclient, "test-cluster2", "default",
				expected{scheduledJob: true, statusScheduled: true, statusActiveSchedule: false})
			checkState(scheduler, k8sclient, "test-cluster3", "default",
				expected{scheduledJob: false, statusScheduled: false, statusActiveSchedule: false})
			checkState(scheduler, k8sclient, "test-cluster4", "default",
				expected{scheduledJob: true, statusScheduled: true, statusActiveSchedule: false})
		})
	})
})

//nolint:unparam // namespace is always default, for now. That may change.
func checkState(
	scheduler quartz.Scheduler,
	k8sclient client.Client,
	cluster, namespace string,
	expectedState expected) {
	isScheduled, err := isClusterScheduled(scheduler, cluster, namespace)
	Expect(err).NotTo(HaveOccurred())
	Expect(isScheduled).To(Equal(expectedState.scheduledJob))

	key := client.ObjectKey{Name: cluster, Namespace: namespace}
	clusterObj := &fleet.Cluster{}
	err = k8sclient.Get(context.Background(), key, clusterObj)
	Expect(err).NotTo(HaveOccurred())
	Expect(clusterObj.Status.Scheduled).To(Equal(expectedState.statusScheduled))
	Expect(clusterObj.Status.ActiveSchedule).To(Equal(expectedState.statusActiveSchedule))
}
