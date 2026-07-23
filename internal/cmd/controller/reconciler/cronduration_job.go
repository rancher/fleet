package reconciler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	fleetutil "github.com/rancher/fleet/internal/cmd/controller/errorutil"
	"github.com/rancher/fleet/internal/cmd/controller/target"
	"github.com/rancher/fleet/internal/cmd/controller/target/matcher"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/reugn/go-quartz/quartz"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CronDurationJob struct {
	Started          bool
	Schedule         *fleet.Schedule
	Scheduler        quartz.Scheduler
	Matcher          *matcher.ScheduleMatch
	MatchingClusters []string
	Location         *time.Location
	client           client.Client
	hash             string
	key              *quartz.JobKey

	// mu guards the fields above against concurrent access by the job's own start and stop
	// actions, which quartz runs in their own goroutine, and by reconciles of the job's Schedule,
	// which may replace or delete the job.
	mu sync.Mutex
	// stale marks a job which has been replaced or deleted by a reconcile. Quartz may already have
	// dispatched a start or stop action for it by then; that action must not run, as it would
	// re-arm this job over the one which replaced it.
	stale bool
}

// newCronDurationJob constructs a new CronDurationJob.
// It also verifies the validity and correctness of the schedule and duration data.
// Internally, it assigns Location = Local if no location was specified in the schedule,
// and from that point onward, any time-related calculations are performed using this location.
func newCronDurationJob(ctx context.Context, schedule *fleet.Schedule, scheduler quartz.Scheduler, c client.Client) (*CronDurationJob, error) {
	locationStr := schedule.Spec.Location
	if locationStr == "" {
		locationStr = "Local"
	}
	location, err := time.LoadLocation(locationStr)
	if err != nil {
		return nil, err
	}

	if err := checkScheduleAndDuration(schedule, location); err != nil {
		return nil, err
	}

	hash, err := getScheduleJobHash(schedule)
	if err != nil {
		return nil, err
	}

	matcher, err := matcher.NewScheduleMatch(schedule)
	if err != nil {
		return nil, err
	}

	matchingClusters, err := matchingClusters(ctx, matcher, c, schedule.Namespace)
	if err != nil {
		return nil, err
	}

	return &CronDurationJob{
		Schedule:         schedule,
		Scheduler:        scheduler,
		Matcher:          matcher,
		MatchingClusters: matchingClusters,
		Location:         location,
		client:           c,
		hash:             hash,
		key:              scheduleKey(schedule),
	}, nil
}

// Execute implements the quartz.Job interface function to run a scheduled job.
func (c *CronDurationJob) Execute(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stale {
		// The job has been replaced or deleted while quartz was dispatching this execution.
		// Whichever job now owns the schedule, if any, is responsible for it.
		return nil
	}

	if c.Started {
		// If the job has already started, this execution is for the "stop" action,
		// which was scheduled to run after the specified duration.
		return c.executeStop(ctx)
	}
	return c.executeStart(ctx)
}

// targets returns true if the job's Schedule lives in the given namespace and currently matches the
// given cluster.
func (c *CronDurationJob) targets(cluster, namespace string) bool {
	if c.Schedule.Namespace != namespace {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.Contains(c.MatchingClusters, cluster)
}

// Description implements the quartz.Job interface function to describe a scheduled job.
func (c *CronDurationJob) Description() string {
	return "CronDurationJob-" + c.hash
}

// scheduleKey builds a quartz.JobKey for the given fleet Schedule
func scheduleKey(schedule *fleet.Schedule) *quartz.JobKey {
	return quartz.NewJobKey(fmt.Sprintf("schedule-%s/%s", schedule.Namespace, schedule.Name))
}

// getScheduleJobHash returns a unique key to identify the given schedule.
// The key is a hash of the json representation of the schedule.
func getScheduleJobHash(sched *fleet.Schedule) (string, error) {
	jsonBytes, err := json.Marshal(sched.Spec)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(jsonBytes)
	return hex.EncodeToString(hash[:]), nil
}

func (c *CronDurationJob) durationToNextStart() (time.Duration, error) {
	cronTrigger, err := quartz.NewCronTriggerWithLoc(c.Schedule.Spec.Schedule, c.Location)
	if err != nil {
		return 0, err
	}
	now := quartz.NowNano()
	nextFireTime, err := cronTrigger.NextFireTime(now)
	if err != nil {
		return 0, err
	}

	return time.Duration(nextFireTime - now), nil
}

// checkScheduleAndDuration verifies that the given schedule start time and the duration are feasible.
// If the duration is longer than 2 consecutive triggers it is considered as non valid.
func checkScheduleAndDuration(schedule *fleet.Schedule, location *time.Location) error {
	trigger, err := quartz.NewCronTriggerWithLoc(schedule.Spec.Schedule, location)
	if err != nil {
		return err
	}
	now := quartz.NowNano()
	firstFireTime, err := trigger.NextFireTime(now)
	if err != nil {
		return err
	}
	firstFireTimeSecs := firstFireTime / 1_000_000_000

	secondFireTime, err := trigger.NextFireTime(firstFireTime)
	if err != nil {
		return err
	}
	secondFireTimeSecs := secondFireTime / 1_000_000_000

	if int64(schedule.Spec.Duration.Seconds()) >= (secondFireTimeSecs - firstFireTimeSecs) {
		// we also consider an error when duration is equal to the next time
		// the job should trigger because we could incur race conditions.
		return errors.New("duration is too long and overlaps with the next execution time")
	}

	return nil
}

func (c *CronDurationJob) scheduleStopJob() error {
	return c.Scheduler.ScheduleJob(
		quartz.NewJobDetailWithOptions(
			c,
			c.key,
			&quartz.JobDetailOptions{
				Replace: true,
			},
		),
		quartz.NewRunOnceTrigger(c.Schedule.Spec.Duration.Duration),
	)
}

func (c *CronDurationJob) scheduleJob(ctx context.Context) error {
	return c.rescheduleJob(ctx)
}

func (c *CronDurationJob) updateJob(ctx context.Context) error {
	return c.rescheduleJob(ctx)
}

func (c *CronDurationJob) rescheduleJob(ctx context.Context) error {
	next, err := c.durationToNextStart()
	if err != nil {
		return err
	}

	if err := c.Scheduler.ScheduleJob(
		quartz.NewJobDetailWithOptions(
			c,
			c.key,
			&quartz.JobDetailOptions{
				Replace: true,
			},
		),
		quartz.NewRunOnceTrigger(next),
	); err != nil {
		return err
	}

	now := time.Now()
	status := fleet.ScheduleStatus{
		Active: false,
		NextStartTime: metav1.Time{
			Time: now.In(c.Location).Add(next),
		},
		MatchingClusters: c.MatchingClusters,
	}

	return setScheduleStatus(ctx, c.client, c.Schedule, status)
}

func (c *CronDurationJob) executeStart(ctx context.Context) error {
	c.Started = true

	// Recalculate matching clusters at execution time. This ensures that any
	// changes to cluster labels that occurred since the last reconciliation
	// are included. The controller's watchers only trigger reconciles for
	// clusters that are already part of a schedule.
	clusters, err := matchingClusters(ctx, c.Matcher, c.client, c.Schedule.Namespace)
	if err != nil {
		return err
	}

	// Sets Scheduled to false for all clusters that previously matched but no longer do.
	for _, cluster := range c.MatchingClusters {
		if !slices.Contains(clusters, cluster) {
			// this cluster is no longer targeted
			if err := setClusterScheduled(ctx, c.client, cluster, c.Schedule.Namespace, false); err != nil {
				return err
			}
		}
	}

	// Sets ActiveSchedule to true for all matching clusters.
	for _, cluster := range clusters {
		if err := setClusterActiveSchedule(ctx, c.client, cluster, c.Schedule.Namespace, true); err != nil {
			return err
		}
	}
	c.MatchingClusters = clusters

	// Update the status of the Schedule resource
	if err := setScheduleActive(ctx, c.client, c.Schedule, true); err != nil {
		return err
	}

	// Schedules the Stop call (now + duration)
	return c.scheduleStopJob()
}

func (c *CronDurationJob) executeStop(ctx context.Context) error {
	c.Started = false

	// Sets ActiveSchedule to false for all matching clusters.
	// This action disables the creation of BundleDeployments on the clusters.
	for _, cluster := range c.MatchingClusters {
		if err := setClusterActiveSchedule(ctx, c.client, cluster, c.Schedule.Namespace, false); err != nil {
			return err
		}
	}

	// Update the status of the Schedule resource
	if err := setScheduleActive(ctx, c.client, c.Schedule, false); err != nil {
		return err
	}

	// Schedules again the job.
	return c.scheduleJob(ctx)
}

// matchingClusters returns the list of clusters that match the given Schedule at this moment.
func matchingClusters(ctx context.Context, matcher *matcher.ScheduleMatch, c client.Client, namespace string) ([]string, error) {
	clusters := &fleet.ClusterList{}
	if err := c.List(ctx, clusters, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("%w, listing clusters: %w", fleetutil.ErrRetryable, err)
	}
	var clusterNames []string
	for _, cluster := range clusters.Items {
		cgs, err := target.ClusterGroupsForCluster(ctx, c, &cluster)
		if err != nil {
			return nil, fmt.Errorf("%w, getting cluster groups from clusters: %w", fleetutil.ErrRetryable, err)
		}

		if matcher.MatchCluster(cluster.Name, target.ClusterGroupsToLabelMap(cgs), cluster.Labels) {
			clusterNames = append(clusterNames, cluster.Name)
		}
	}

	return clusterNames, nil
}
