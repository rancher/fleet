package reconciler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	fleetutil "github.com/rancher/fleet/internal/cmd/controller/errorutil"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"
	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/reugn/go-quartz/quartz"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	scheduleFinalizer = "fleet.cattle.io/schedule-finalizer"
)

// ScheduleReconciler reconciles a Schedule object
type ScheduleReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	ShardID   string
	Workers   int
	Scheduler quartz.Scheduler
}

//+kubebuilder:rbac:groups=fleet.cattle.io,resources=schedules,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=schedules/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=schedules/finalizers,verbs=update
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clusters,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clustergroups,verbs=get;list;watch

// SetupWithManager sets up the controller with the Manager.
func (r *ScheduleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.Schedule{},
			builder.WithPredicates(
				predicate.GenerationChangedPredicate{},
			),
		).
		Watches(
			&fleet.Cluster{},
			handler.EnqueueRequestsFromMapFunc(r.mapClustersToSchedules),
			builder.WithPredicates(clusterChangedPredicate()),
		).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)
}

func (r *ScheduleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("schedule")

	schedule := &fleet.Schedule{}
	if err := r.Get(ctx, req.NamespacedName, schedule); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !schedule.DeletionTimestamp.IsZero() {
		return r.handleDelete(ctx, schedule)
	}

	if err := r.ensureFinalizer(ctx, schedule); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Reconciling Schedule")

	if err := r.handleSchedule(ctx, schedule); err != nil {
		// If the error is retryable (e.g., a transient k8s client error),
		// return it to trigger a requeue by the controller runtime.
		if errors.Is(err, fleetutil.ErrRetryable) {
			return ctrl.Result{}, err
		}

		setScheduleReadyCondition(&schedule.Status, err)
		return ctrl.Result{}, r.Client.Status().Update(ctx, schedule)
	}

	return ctrl.Result{}, nil
}

func (r *ScheduleReconciler) handleSchedule(ctx context.Context, s *fleet.Schedule) error {
	k := scheduleKey(s)
	schedJob, err := r.Scheduler.GetScheduledJob(k)
	if err != nil && !errors.Is(err, quartz.ErrJobNotFound) {
		return fmt.Errorf("an unknown error occurred when looking for a schedule job: %w", err)
	}

	if errors.Is(err, quartz.ErrJobNotFound) {
		return scheduleNewCronDurationJob(ctx, s, r.Scheduler, r.Client)
	}

	// the job already exists, check if an update is needed
	newJob, err := newCronDurationJob(ctx, s, r.Scheduler, r.Client)
	if err != nil {
		return err
	}
	existingJob := schedJob.JobDetail().Job()
	existingCronJob, ok := existingJob.(*CronDurationJob)
	if !ok {
		return fmt.Errorf("unexpected job found for key: %s", k.String())
	}

	if jobNeedsUpdate(newJob, existingCronJob) {
		if err := newJob.updateJob(ctx); err != nil {
			return err
		}
		return updateScheduledClusters(
			ctx,
			r.Scheduler,
			r.Client,
			newJob.MatchingClusters,
			existingCronJob.MatchingClusters,
			s.Namespace,
		)
	}
	return nil
}

func (r *ScheduleReconciler) handleDelete(ctx context.Context, schedule *fleet.Schedule) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(schedule, scheduleFinalizer) {
		return ctrl.Result{}, nil
	}

	if err := deleteSchedule(ctx, schedule, r.Scheduler); err != nil {
		return ctrl.Result{}, err
	}

	controllerutil.RemoveFinalizer(schedule, scheduleFinalizer)
	if err := r.Update(ctx, schedule); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ScheduleReconciler) ensureFinalizer(ctx context.Context, schedule *fleet.Schedule) error {
	if controllerutil.ContainsFinalizer(schedule, scheduleFinalizer) {
		return nil
	}
	controllerutil.AddFinalizer(schedule, scheduleFinalizer)
	return r.Update(ctx, schedule)
}

// mapClustersToSchedules is a mapping function used to trigger a reconciliation of Schedules
// when a targeted Cluster changes. It finds all schedules that target the cluster
// and enqueues a reconcile request for each of them.
func (r *ScheduleReconciler) mapClustersToSchedules(ctx context.Context, a client.Object) []ctrl.Request {
	ns := a.GetNamespace()
	logger := log.FromContext(ctx).WithName("cluster-scheduler-handler").WithValues("namespace", ns)
	cluster := a.(*fleet.Cluster)

	// check if the cluster is scheduled
	schedules, err := getClusterSchedules(r.Scheduler, cluster.Name, cluster.Namespace)
	if err != nil {
		logger.Error(err, "Failed to get cluster schedules")
		return nil
	}
	requests := []ctrl.Request{}
	for _, schedule := range schedules {
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: ns,
				Name:      schedule.Name,
			},
		})
	}

	return requests
}

// jobNeedsUpdate returns true if there is a discrepancy between newJob and existingJob:
// * either in their descriptions, indicating that the parent schedule for those jobs has been updated
// * or in clusters matched by the jobs, which may result from updates to the clusters themselves,
// or creation/deletion of clusters since the existingJob was created.
func jobNeedsUpdate(newJob, existingJob *CronDurationJob) bool {
	return newJob.Description() != existingJob.Description() ||
		!slices.Equal(newJob.MatchingClusters, existingJob.MatchingClusters)
}

func scheduleNewCronDurationJob(ctx context.Context, s *fleet.Schedule, scheduler quartz.Scheduler, c client.Client) error {
	job, err := newCronDurationJob(ctx, s, scheduler, c)
	if err != nil {
		return err
	}

	if err := job.scheduleJob(ctx); err != nil {
		return err
	}

	return setClustersScheduled(ctx, c, job.MatchingClusters, s.Namespace, true)
}

func deleteSchedule(ctx context.Context, s *fleet.Schedule, scheduler quartz.Scheduler) error {
	k := scheduleKey(s)
	schedJob, err := scheduler.GetScheduledJob(k)
	if err != nil && !errors.Is(err, quartz.ErrJobNotFound) {
		return fmt.Errorf("an unknown error occurred when trying to delete a schedule job: %w", err)
	}
	if errors.Is(err, quartz.ErrJobNotFound) {
		return nil
	}

	cronDurationJob, ok := schedJob.JobDetail().Job().(*CronDurationJob)
	if !ok {
		return fmt.Errorf("found an unexpected job type for key: %s", k.String())
	}

	if err := scheduler.DeleteJob(k); err != nil {
		return err
	}

	// get the list of clusters that are no longer in any schedule
	noLongerScheduled, err := getClustersNotScheduled(scheduler, cronDurationJob.MatchingClusters, s.Namespace)
	if err != nil {
		return err
	}

	// set the Scheduled property of those not scheduled to false
	return setClustersScheduled(ctx, cronDurationJob.client, noLongerScheduled, s.Namespace, false)
}

func setClusterActiveSchedule(ctx context.Context, c client.Client, name, namespace string, active bool) error {
	key := client.ObjectKey{Name: name, Namespace: namespace}
	cluster := &fleet.Cluster{}
	if err := c.Get(ctx, key, cluster); err != nil {
		return fmt.Errorf("%w, getting cluster: %w", fleetutil.ErrRetryable, err)
	}

	// if the values are already the expected ones, avoid the update
	if cluster.Status.Scheduled && cluster.Status.ActiveSchedule == active {
		return nil
	}
	old := cluster.DeepCopy()
	cluster.Status.ActiveSchedule = active
	cluster.Status.Scheduled = true

	return updateClusterStatus(ctx, c, old, cluster)
}

func setClusterScheduled(ctx context.Context, c client.Client, name, namespace string, scheduled bool) error {
	key := client.ObjectKey{Name: name, Namespace: namespace}
	cluster := &fleet.Cluster{}
	if err := c.Get(ctx, key, cluster); err != nil {
		return fmt.Errorf("%w, getting cluster: %w", fleetutil.ErrRetryable, err)
	}

	// if the values are already the expected ones, avoid the update
	if cluster.Status.Scheduled == scheduled && !cluster.Status.ActiveSchedule {
		return nil
	}

	old := cluster.DeepCopy()
	cluster.Status.Scheduled = scheduled

	// This function is called either because we're updating a
	// Schedule or because we're creating it.
	// In both cases ActiveSchedule should be false as a Schedule
	// always begins in OffSchedule mode until the first start call is executed.
	cluster.Status.ActiveSchedule = false

	return updateClusterStatus(ctx, c, old, cluster)
}

func setScheduleActive(ctx context.Context, c client.Client, schedule *fleet.Schedule, active bool) error {
	// if the value is already the expected one, avoid the update
	if schedule.Status.Active == active {
		return nil
	}
	old := schedule.DeepCopy()
	schedule.Status.Active = active

	return updateScheduleStatus(ctx, c, old, schedule)
}

func setScheduleStatus(ctx context.Context, c client.Client, schedule *fleet.Schedule, status fleet.ScheduleStatus) error {
	old := schedule.DeepCopy()
	schedule.Status = status

	return updateScheduleStatus(ctx, c, old, schedule)
}

// setScheduleReadyCondition sets the Ready condition on the status and updates the timestamp if the condition has changed.
func setScheduleReadyCondition(status *fleet.ScheduleStatus, err error) {
	if status == nil {
		status = &fleet.ScheduleStatus{}
	}
	cond := condition.Cond(fleet.Ready)
	origStatus := status.DeepCopy()
	cond.SetError(status, "", err)
	if !equality.Semantic.DeepEqual(origStatus, status) {
		cond.LastUpdated(status, time.Now().UTC().Format(time.RFC3339))
	}
}

func updateClusterStatus(ctx context.Context, c client.Client, old *fleet.Cluster, new *fleet.Cluster) error {
	nsn := types.NamespacedName{Name: new.Name, Namespace: new.Namespace}
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster := &fleet.Cluster{}
		if err := c.Get(ctx, nsn, cluster); err != nil {
			return fmt.Errorf("could not get Cluster to update its status: %w", err)
		}
		cluster.Status.Scheduled = new.Status.Scheduled
		cluster.Status.ActiveSchedule = new.Status.ActiveSchedule
		statusPatch := client.MergeFrom(old)
		if patchData, err := statusPatch.Data(cluster); err == nil && string(patchData) == "{}" {
			// skip update if patch is empty
			return nil
		}
		return c.Status().Patch(ctx, cluster, statusPatch)
	}); err != nil {
		return fmt.Errorf("%w, updating cluster status: %w", fleetutil.ErrRetryable, err)
	}
	return nil
}

func updateScheduleStatus(ctx context.Context, c client.Client, old *fleet.Schedule, new *fleet.Schedule) error {
	nsn := types.NamespacedName{Name: new.Name, Namespace: new.Namespace}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		schedule := &fleet.Schedule{}
		if err := c.Get(ctx, nsn, schedule); err != nil {
			return fmt.Errorf("could not get Schedule to update its status: %w", err)
		}

		schedule.Status = new.Status

		statusPatch := client.MergeFrom(old)
		if patchData, err := statusPatch.Data(schedule); err == nil && string(patchData) == "{}" {
			return nil
		}
		return c.Status().Patch(ctx, schedule, statusPatch)
	})
}

// isClusterScheduled returns true if the given cluster is part of
// any scheduled job as a matching cluster.
func isClusterScheduled(scheduler quartz.Scheduler, cluster, namespace string) (bool, error) {
	keys, err := getClusterScheduleKeys(scheduler, cluster, namespace)
	if err != nil {
		return false, err
	}

	return len(keys) != 0, nil
}

// getClusterSchedules returns all the fleet Schedules in which the given cluster is found as a matching target.
func getClusterSchedules(scheduler quartz.Scheduler, cluster, namespace string) ([]*fleet.Schedule, error) {
	keys, err := getClusterScheduleKeys(scheduler, cluster, namespace)
	if err != nil {
		return nil, err
	}

	schedules := []*fleet.Schedule{}
	for _, key := range keys {
		job, err := scheduler.GetScheduledJob(key)
		if err != nil {
			return nil, err
		}
		cronDurationJob, ok := job.JobDetail().Job().(*CronDurationJob)
		if !ok {
			return nil, fmt.Errorf("unexpected job type for key: %s", key.String())
		}
		schedules = append(schedules, cronDurationJob.Schedule)
	}

	return schedules, nil
}

// getClustersNotScheduled returns the list of the given clusters
// that are not part of any scheduled job.
func getClustersNotScheduled(scheduler quartz.Scheduler, clusters []string, namespace string) ([]string, error) {
	notScheduled := []string{}
	for _, cluster := range clusters {
		scheduled, err := isClusterScheduled(scheduler, cluster, namespace)
		if err != nil {
			return nil, err
		}
		if !scheduled {
			notScheduled = append(notScheduled, cluster)
		}
	}

	return notScheduled, nil
}

func setClustersScheduled(ctx context.Context, c client.Client, clusters []string, namespace string, scheduled bool) error {
	for _, cluster := range clusters {
		if err := setClusterScheduled(ctx, c, cluster, namespace, scheduled); err != nil {
			return err
		}
	}

	return nil
}

func updateScheduledClusters(ctx context.Context, scheduler quartz.Scheduler, c client.Client, clustersNew []string, clustersOld []string, namespace string) error {
	for _, cluster := range clustersNew {
		if err := setClusterScheduled(ctx, c, cluster, namespace, true); err != nil {
			return err
		}
	}

	// now check for clusters that are flagged as scheduled and should no longer be flagged because
	// they are no longer targeted by any schedule
	for _, cluster := range clustersOld {
		if !slices.Contains(clustersNew, cluster) {
			targeted, err := isClusterScheduled(scheduler, cluster, namespace)
			if err != nil {
				return err
			}
			if !targeted {
				if err := setClusterScheduled(ctx, c, cluster, namespace, false); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
