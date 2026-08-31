package reconciler

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	fleetutil "github.com/rancher/fleet/internal/cmd/controller/errorutil"
	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/cmd/controller/target/matcher"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"
	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/reugn/go-quartz/quartz"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
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

// ScheduleReconciler reconciles a Schedule object
type ScheduleReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  events.EventRecorder
	ShardID   string
	Workers   int
	Scheduler quartz.Scheduler
	jobs      jobRegistry
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
				sharding.FilterByShardID(r.ShardID),
			),
		).
		Watches(
			&fleet.Cluster{},
			handler.EnqueueRequestsFromMapFunc(r.mapClustersToSchedules),
			builder.WithPredicates(clusterChangedPredicate()),
			// Deliberately skipping the sharding filter here: a schedule may live in the namespace of a cluster with both
			// bearing distinct shard IDs. Instead, mapClustersToSchedules maps clusters to schedules in the
			// current shard only.
		).
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

	if err := finalize.EnsureFinalizer(ctx, r.Client, schedule, finalize.ScheduleFinalizer); err != nil {
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

	job, found := r.jobs.get(k)
	if !found {
		return r.scheduleNewCronDurationJob(ctx, s)
	}

	// The job exists: hold its lock for the whole update
	job.mu.Lock()
	defer job.mu.Unlock()

	newJob, err := newCronDurationJob(ctx, s, r.Scheduler, r.Client)
	if err != nil {
		return err
	}

	if !jobNeedsUpdate(newJob, job) {
		return nil
	}

	if err := newJob.updateJob(ctx); err != nil {
		return err
	}

	job.stale = true
	r.jobs.store(k, newJob)

	return r.updateScheduledClusters(ctx, newJob.MatchingClusters, job.MatchingClusters, s.Namespace)
}

func (r *ScheduleReconciler) handleDelete(ctx context.Context, schedule *fleet.Schedule) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(schedule, finalize.ScheduleFinalizer) {
		return ctrl.Result{}, nil
	}

	if err := r.deleteSchedule(ctx, schedule); err != nil {
		return ctrl.Result{}, err
	}

	controllerutil.RemoveFinalizer(schedule, finalize.ScheduleFinalizer)
	if err := r.Update(ctx, schedule); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// mapClustersToSchedules is a mapping function used to trigger a reconciliation of Schedules
// when a targeted Cluster changes. It finds all schedules in r's shard that target the cluster
// and enqueues a reconcile request for each of them.
func (r *ScheduleReconciler) mapClustersToSchedules(ctx context.Context, a client.Object) []ctrl.Request {
	ns := a.GetNamespace()
	logger := log.FromContext(ctx).WithName("cluster-scheduler-handler").WithValues("namespace", ns)
	cluster := a.(*fleet.Cluster)

	// check if the cluster is scheduled
	schedules, err := r.getClusterSchedules(ctx, cluster)
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

func (r *ScheduleReconciler) scheduleNewCronDurationJob(ctx context.Context, s *fleet.Schedule) error {
	job, err := newCronDurationJob(ctx, s, r.Scheduler, r.Client)
	if err != nil {
		return err
	}

	k := scheduleKey(s)
	// Register the job before arming it, so that it is never running while unknown to the registry.
	r.jobs.store(k, job)

	if err := job.scheduleJob(ctx); err != nil {
		r.jobs.delete(k)
		return err
	}

	return setClustersScheduled(ctx, r.Client, job.MatchingClusters, s.Namespace, true)
}

func (r *ScheduleReconciler) deleteSchedule(ctx context.Context, s *fleet.Schedule) error {
	k := scheduleKey(s)
	job, found := r.jobs.get(k)
	if !found {
		return nil
	}

	job.mu.Lock()
	job.stale = true
	clusters := slices.Clone(job.MatchingClusters)
	job.mu.Unlock()

	// A job which quartz has popped for execution is not in the scheduler's queue, so there may be
	// nothing to delete there. Marking it stale above is what stops it in that case.
	if err := r.Scheduler.DeleteJob(k); err != nil && !errors.Is(err, quartz.ErrJobNotFound) {
		return fmt.Errorf("an unknown error occurred when trying to delete a schedule job: %w", err)
	}
	r.jobs.delete(k)

	// get the list of clusters that are no longer in any schedule
	noLongerScheduled := r.jobs.clustersNotScheduled(clusters, s.Namespace)

	// set the Scheduled property of those not scheduled to false
	return setClustersScheduled(ctx, r.Client, noLongerScheduled, s.Namespace, false)
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

// getClusterSchedules returns all the fleet Schedules with a matching shardID, in which the given cluster is found as a
// matching target. To this end, it looks at two sources of data:
// * jobs which already target the cluster
// * schedules which targets match the cluster, to include schedules for which no job may have been scheduled yet.
func (r *ScheduleReconciler) getClusterSchedules(
	ctx context.Context,
	cluster *fleet.Cluster,
) ([]*fleet.Schedule, error) {
	schedules := []*fleet.Schedule{}
	scheduleNames := map[string]struct{}{}
	for _, job := range r.jobs.jobsForCluster(cluster.Name, cluster.Namespace) {
		if !sharding.ShouldProcess(job.Schedule, r.ShardID) {
			continue
		}

		schedules = append(schedules, job.Schedule)
		scheduleNames[job.Schedule.Name] = struct{}{}
	}

	// Consider schedules which may exist but for which no job may have been created yet.
	allSchedules := &fleet.ScheduleList{}
	if err := r.List(ctx, allSchedules, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, fmt.Errorf("%w, listing schedules: %w", fleetutil.ErrRetryable, err)
	}

	groups, err := target.ClusterGroupsForCluster(ctx, r.Client, cluster)
	if err != nil {
		return nil, fmt.Errorf("%w, getting cluster groups from clusters: %w", fleetutil.ErrRetryable, err)
	}

	cgs := target.ClusterGroupsToLabelMap(groups)

	for i, s := range allSchedules.Items {
		if !sharding.ShouldProcess(&s, r.ShardID) {
			continue
		}

		// Skip already found schedules, to prevent duplicates and unnecessary computations.
		if _, alreadyFound := scheduleNames[s.Name]; alreadyFound {
			continue
		}

		matcher, err := matcher.NewScheduleMatch(&s)
		if err != nil {
			return nil, err
		}

		if matcher.MatchCluster(cluster.Name, cgs, cluster.Labels) {
			schedules = append(schedules, &allSchedules.Items[i])
		}
	}

	return schedules, nil
}

func setClustersScheduled(ctx context.Context, c client.Client, clusters []string, namespace string, scheduled bool) error {
	for _, cluster := range clusters {
		if err := setClusterScheduled(ctx, c, cluster, namespace, scheduled); err != nil {
			return err
		}
	}

	return nil
}

func (r *ScheduleReconciler) updateScheduledClusters(ctx context.Context, clustersNew []string, clustersOld []string, namespace string) error {
	for _, cluster := range clustersNew {
		if err := setClusterScheduled(ctx, r.Client, cluster, namespace, true); err != nil {
			return err
		}
	}

	// now check for clusters that are flagged as scheduled and should no longer be flagged because
	// they are no longer targeted by any schedule
	for _, cluster := range clustersOld {
		if slices.Contains(clustersNew, cluster) {
			continue
		}
		if r.jobs.isClusterScheduled(cluster, namespace) {
			continue
		}
		if err := setClusterScheduled(ctx, r.Client, cluster, namespace, false); err != nil {
			return err
		}
	}
	return nil
}
