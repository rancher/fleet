package reconciler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/reugn/go-quartz/quartz"

	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	"github.com/rancher/fleet/internal/cmd/controller/imagescan"
	ctrlquartz "github.com/rancher/fleet/internal/cmd/controller/quartz"
	"github.com/rancher/fleet/internal/cmd/controller/reconciler"
	"github.com/rancher/fleet/internal/metrics"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	fleetevent "github.com/rancher/fleet/pkg/event"
	"github.com/rancher/fleet/pkg/sharding"

	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	"github.com/rancher/wrangler/v3/pkg/kstatus"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/cli-utils/pkg/kstatus/status"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	defaultPollingSyncInterval = 15 * time.Second
	gitPollingCondition        = "GitPolling"
	generationLabel            = "fleet.cattle.io/gitrepo-generation"
	forceSyncGenerationLabel   = "fleet.cattle.io/force-sync-generation"
	// The TTL is the grace period for short-lived metrics to be kept alive to
	// make sure Prometheus scrapes them.
	ShortLivedMetricsTTL       = 120 * time.Second
	gitJobPollingJitterPercent = 10
)

var (
	GitJobDurationBuckets = []float64{1, 2, 5, 10, 30, 60, 180, 300, 600, 1200, 1800, 3600}
	gitjobsCreatedSuccess = metrics.ObjCounter(
		"gitjobs_created_success_total",
		"Total number of failed git job creations",
	)
	gitjobsCreatedFailure = metrics.ObjCounter(
		"gitjobs_created_failure_total",
		"Total number of successfully created git jobs",
	)
	gitjobDuration = metrics.ObjHistogram(
		"gitjob_duration_seconds",
		"Duration to complete a Git job in seconds. Includes the time to fetch the git repo and to create the bundle.",
		GitJobDurationBuckets,
	)
	gitjobDurationGauge = metrics.ObjGauge(
		"gitjob_duration_seconds_gauge",
		"Duration to complete a Git job in seconds. Includes the time to fetch the git repo and to create the bundle.",
	)
	fetchLatestCommitSuccess = metrics.ObjCounter(
		"gitrepo_fetch_latest_commit_success_total",
		"Total number of successful fetches of latest commit",
	)
	fetchLatestCommitFailure = metrics.ObjCounter(
		"gitrepo_fetch_latest_commit_failure_total",
		"Total number of failed attempts to retrieve the latest commit, for any reason",
	)
	timeToFetchLatestCommit = metrics.ObjHistogram(
		"gitrepo_fetch_latest_commit_duration_seconds",
		"Duration in seconds to fetch the latest commit",
		metrics.BucketsLatency,
	)
)

type GitFetcher interface {
	LatestCommit(ctx context.Context, gitrepo *v1alpha1.GitRepo, client client.Client) (string, error)
}

// TimeGetter interface is used to mock the time.Now() call in unit tests
type TimeGetter interface {
	Now() time.Time
	Since(t time.Time) time.Duration
}

type RealClock struct{}

func (RealClock) Now() time.Time                  { return time.Now() }
func (RealClock) Since(t time.Time) time.Duration { return time.Since(t) }

type KnownHostsGetter interface {
	Get(ctx context.Context, client client.Client, namespace, secretName string) (string, error)
	IsStrict() bool
}

// GitJobReconciler reconciles a GitRepo resource to create a git cloning k8s job
type GitJobReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	Image           string
	Scheduler       quartz.Scheduler
	Workers         int
	ShardID         string
	JobNodeSelector string
	GitFetcher      GitFetcher
	Clock           TimeGetter
	Recorder        record.EventRecorder
	SystemNamespace string
	KnownHosts      KnownHostsGetter
}

func (r *GitJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.GitRepo{},
			builder.WithPredicates(
				// do not trigger for GitRepo status changes (except for commit changes and cache sync)
				predicate.Or(
					reconciler.TypedResourceVersionUnchangedPredicate[client.Object]{},
					predicate.GenerationChangedPredicate{},
					predicate.AnnotationChangedPredicate{},
					predicate.LabelChangedPredicate{},
					commitChangedPredicate(),
				),
			),
		).
		Owns(&batchv1.Job{}, builder.WithPredicates(jobUpdatedPredicate())).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)
}

// Reconcile  compares the state specified by the GitRepo object against the
// actual cluster state. It checks the Git repository for new commits and
// creates a job to clone the repository if a new commit is found. In case of
// an error, the output of the job is stored in the status.
func (r *GitJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("gitjob")
	gitrepo := &v1alpha1.GitRepo{}

	if err := r.Get(ctx, req.NamespacedName, gitrepo); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	} else if apierrors.IsNotFound(err) {
		gitjobsCreatedSuccess.DeleteByReq(req)
		gitjobsCreatedFailure.DeleteByReq(req)
		gitjobDuration.DeleteByReq(req)
		fetchLatestCommitSuccess.DeleteByReq(req)
		fetchLatestCommitFailure.DeleteByReq(req)
		timeToFetchLatestCommit.DeleteByReq(req)

		logger.V(1).Info("Gitrepo deleted, cleaning up pull jobs")
		return ctrl.Result{}, nil
	}

	// Restrictions / Overrides, gitrepo reconciler is responsible for setting error in status
	oldStatus := gitrepo.Status.DeepCopy()
	if err := AuthorizeAndAssignDefaults(ctx, r.Client, gitrepo); err != nil {
		r.Recorder.Event(gitrepo, fleetevent.Warning, "FailedToApplyRestrictions", err.Error())
		return ctrl.Result{}, updateErrorStatus(ctx, r.Client, req.NamespacedName, *oldStatus, err)
	}

	if !gitrepo.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(gitrepo, finalize.GitRepoFinalizer) {
			if err := r.cleanupGitRepo(ctx, logger, gitrepo); err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	if err := finalize.EnsureFinalizer(ctx, r.Client, gitrepo, finalize.GitRepoFinalizer); err != nil {
		return ctrl.Result{}, err
	}

	// Migration: Remove the obsolete created-by-display-name label if it exists
	if err := r.removeDisplayNameLabel(ctx, req.NamespacedName); err != nil {
		logger.V(1).Error(err, "Failed to remove display name label")
		return ctrl.Result{}, err
	}

	logger = logger.WithValues("generation", gitrepo.Generation, "commit", gitrepo.Status.Commit).WithValues("conditions", gitrepo.Status.Conditions)

	if userID := gitrepo.Labels[v1alpha1.CreatedByUserIDLabel]; userID != "" {
		logger = logger.WithValues("userID", userID)
	}

	ctx = log.IntoContext(ctx, logger)

	logger.V(1).Info("Reconciling GitRepo")

	if gitrepo.Spec.Repo == "" {
		if err := r.deletePollingJob(*gitrepo); err != nil {
			return ctrl.Result{}, updateErrorStatus(ctx, r.Client, req.NamespacedName, gitrepo.Status, err)
		}
		// TODO: return an error here, similar to what we already do for HelmOps
		return ctrl.Result{}, nil
	}

	jobUpdatedOrCreated, err := r.managePollingJob(logger, *gitrepo)
	if err != nil {
		return ctrl.Result{}, updateErrorStatus(ctx, r.Client, req.NamespacedName, gitrepo.Status, err)
	}

	if jobUpdatedOrCreated {
		// Maybe an update from the polling job will come next
		// Requeue and stop this reconcile now as moving on to gitJob creation would
		// possibly lead to conflicts.
		return ctrl.Result{RequeueAfter: durations.DefaultRequeueAfter}, nil
	}

	oldCommit := gitrepo.Status.Commit
	// maybe update the commit from webhooks or polling
	gitrepo.Status.Commit = getNextCommit(gitrepo.Status)

	res, err := r.manageGitJob(ctx, logger, gitrepo, oldCommit)
	if err != nil || res.RequeueAfter > 0 {
		return res, updateErrorStatus(ctx, r.Client, req.NamespacedName, gitrepo.Status, err)
	}

	reconciler.SetCondition(v1alpha1.GitRepoAcceptedCondition, &gitrepo.Status, nil)

	err = updateStatus(ctx, r.Client, req.NamespacedName, gitrepo.Status)
	if err != nil {
		logger.Error(err, "Reconcile failed final update to git repo status", "status", gitrepo.Status)

		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func monitorLatestCommit(obj metav1.Object, fetch func() (string, error)) (string, error) {
	start := time.Now()
	commit, err := fetch()
	if err != nil {
		fetchLatestCommitFailure.Inc(obj)
		return "", err
	}
	fetchLatestCommitSuccess.Inc(obj)
	timeToFetchLatestCommit.Observe(obj, time.Since(start).Seconds())
	return commit, nil
}

// manageGitJob is responsible for creating, updating and deleting the GitJob and setting the GitRepo's status accordingly
func (r *GitJobReconciler) manageGitJob(ctx context.Context, logger logr.Logger, gitrepo *v1alpha1.GitRepo, oldCommit string) (ctrl.Result, error) {
	if err := r.deletePreviousJob(ctx, logger, *gitrepo, oldCommit); err != nil {
		return ctrl.Result{}, err
	}

	var job batchv1.Job
	err := r.Get(ctx, types.NamespacedName{
		Namespace: gitrepo.Namespace,
		Name:      jobName(gitrepo),
	}, &job)
	if err != nil && !apierrors.IsNotFound(err) {
		err = fmt.Errorf("error retrieving git job: %w", err)
		r.Recorder.Event(gitrepo, fleetevent.Warning, "FailedToGetGitJob", err.Error())

		return ctrl.Result{}, err
	}

	if apierrors.IsNotFound(err) {
		if gitrepo.Spec.DisablePolling {
			commit, err := monitorLatestCommit(gitrepo, func() (string, error) {
				return r.GitFetcher.LatestCommit(ctx, gitrepo, r.Client)
			})
			condition.Cond(gitPollingCondition).SetError(&gitrepo.Status, "", err)
			if err == nil && commit != "" {
				gitrepo.Status.Commit = commit
			}
			if err != nil {
				r.Recorder.Event(gitrepo, fleetevent.Warning, "Failed", err.Error())
			} else if oldCommit != gitrepo.Status.Commit {
				r.Recorder.Event(gitrepo, fleetevent.Normal, "GotNewCommit", gitrepo.Status.Commit)
			}
		}

		if r.shouldCreateJob(gitrepo, oldCommit) {
			r.updateGenerationValuesIfNeeded(gitrepo)
			if err := r.validateExternalSecretExist(ctx, gitrepo); err != nil {
				r.Recorder.Event(gitrepo, fleetevent.Warning, "FailedValidatingSecret", err.Error())
				return ctrl.Result{}, fmt.Errorf("error validating external secrets: %w", err)
			}
			if err := r.createJobAndResources(ctx, gitrepo, logger); err != nil {
				gitjobsCreatedFailure.Inc(gitrepo)
				return ctrl.Result{}, err
			}
			gitjobsCreatedSuccess.Inc(gitrepo)
		}
	} else if gitrepo.Status.Commit != "" && gitrepo.Status.Commit == oldCommit {
		err, recreateGitJob := r.deleteJobIfNeeded(ctx, gitrepo, &job)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("error deleting git job: %w", err)
		}
		// job was deleted and we need to recreate it
		// Requeue so the reconciler creates the job again
		if recreateGitJob {
			return ctrl.Result{RequeueAfter: durations.DefaultRequeueAfter}, nil
		}
	}

	gitrepo.Status.ObservedGeneration = gitrepo.Generation

	if err = setStatusFromGitjob(ctx, r.Client, gitrepo, &job); err != nil {
		return ctrl.Result{}, fmt.Errorf("error setting GitRepo status from git job: %w", err)
	}

	return ctrl.Result{}, nil
}

func (r *GitJobReconciler) deletePreviousJob(ctx context.Context, logger logr.Logger, gitrepo v1alpha1.GitRepo, oldCommit string) error {
	if oldCommit == "" || oldCommit == gitrepo.Status.Commit {
		return nil
	}

	// the GitRepo is passed by value, just use the old commit
	// to calculate the job Name
	gitrepo.Status.Commit = oldCommit

	var job batchv1.Job
	err := r.Get(ctx, types.NamespacedName{
		Namespace: gitrepo.Namespace,
		Name:      jobName(&gitrepo),
	}, &job)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}

		return nil
	}

	// At this point we know the previous job still exists and the commit already changed.
	// Delete the previous one so we don't incur in conflicts
	logger.Info("Deleting previous job to avoid conflicts")
	return r.Delete(ctx, &job)
}

func (r *GitJobReconciler) cleanupGitRepo(ctx context.Context, logger logr.Logger, gitrepo *v1alpha1.GitRepo) error {
	logger.Info("Gitrepo deleted, deleting bundle, image scans")

	metrics.GitRepoCollector.Delete(gitrepo.Name, gitrepo.Namespace)
	_ = r.deletePollingJob(*gitrepo)

	nsName := types.NamespacedName{Name: gitrepo.Name, Namespace: gitrepo.Namespace}
	if err := finalize.PurgeBundles(ctx, r.Client, nsName, v1alpha1.RepoLabel); err != nil {
		return err
	}

	// remove the job scheduled by imagescan, if any
	_ = r.Scheduler.DeleteJob(imagescan.GitCommitKey(gitrepo.Namespace, gitrepo.Name))

	if err := finalize.PurgeImageScans(ctx, r.Client, nsName); err != nil {
		return err
	}

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := r.Get(ctx, nsName, gitrepo); err != nil {
			return err
		}

		controllerutil.RemoveFinalizer(gitrepo, finalize.GitRepoFinalizer)

		return r.Update(ctx, gitrepo)
	})

	if client.IgnoreNotFound(err) != nil {
		return err
	}

	return nil
}

// shouldCreateJob checks if the conditions to create a new job are met.
// It checks for all the conditions so, in case more than one is met, it sets all the
// values related in one single reconciler loop
func (r *GitJobReconciler) shouldCreateJob(gitrepo *v1alpha1.GitRepo, oldCommit string) bool {
	if gitrepo.Status.Commit != "" && gitrepo.Status.Commit != oldCommit {
		return true
	}

	if gitrepo.Spec.ForceSyncGeneration != gitrepo.Status.UpdateGeneration {
		return true
	}

	// k8s Jobs are immutable. Recreate the job if the GitRepo Spec has changed.
	// Avoid deleting the job twice
	if generationChanged(gitrepo) {
		return true
	}

	return false
}

func (r *GitJobReconciler) updateGenerationValuesIfNeeded(gitrepo *v1alpha1.GitRepo) {
	if gitrepo.Spec.ForceSyncGeneration != gitrepo.Status.UpdateGeneration {
		gitrepo.Status.UpdateGeneration = gitrepo.Spec.ForceSyncGeneration
	}

	if generationChanged(gitrepo) {
		gitrepo.Status.ObservedGeneration = gitrepo.Generation
	}
}

// removeDisplayNameLabel removes the obsolete created-by-display-name label from the gitrepo if it exists.
func (r *GitJobReconciler) removeDisplayNameLabel(ctx context.Context, nsName types.NamespacedName) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gitrepo := &v1alpha1.GitRepo{}
		if err := r.Get(ctx, nsName, gitrepo); err != nil {
			return err
		}

		if gitrepo.Labels == nil {
			return nil
		}

		const deprecatedLabel = "fleet.cattle.io/created-by-display-name"
		if _, exists := gitrepo.Labels[deprecatedLabel]; !exists {
			return nil
		}

		delete(gitrepo.Labels, deprecatedLabel)
		return r.Update(ctx, gitrepo)
	})
}

func (r *GitJobReconciler) validateExternalSecretExist(ctx context.Context, gitrepo *v1alpha1.GitRepo) error {
	if gitrepo.Spec.HelmSecretNameForPaths != "" {
		if err := r.Get(ctx, types.NamespacedName{Namespace: gitrepo.Namespace, Name: gitrepo.Spec.HelmSecretNameForPaths}, &corev1.Secret{}); err != nil {
			return fmt.Errorf("failed to look up HelmSecretNameForPaths, error: %w", err)
		}
	} else if gitrepo.Spec.HelmSecretName != "" {
		if err := r.Get(ctx, types.NamespacedName{Namespace: gitrepo.Namespace, Name: gitrepo.Spec.HelmSecretName}, &corev1.Secret{}); err != nil {
			return fmt.Errorf("failed to look up helmSecretName, error: %w", err)
		}
	}
	return nil
}

func (r *GitJobReconciler) deleteJobIfNeeded(ctx context.Context, gitRepo *v1alpha1.GitRepo, job *batchv1.Job) (error, bool) {
	logger := log.FromContext(ctx)

	// the following cases imply that the job is still running but we need to stop it and
	// create a new one
	if gitRepo.Spec.ForceSyncGeneration != gitRepo.Status.UpdateGeneration {
		if forceSync, ok := job.Labels[forceSyncGenerationLabel]; ok {
			t := fmt.Sprintf("%d", gitRepo.Spec.ForceSyncGeneration)
			if t != forceSync {
				jobDeletedMessage := "job deletion triggered because of ForceUpdateGeneration"
				logger.V(1).Info(jobDeletedMessage)
				if err := r.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil && !apierrors.IsNotFound(err) {
					return err, true
				}
				return nil, true
			}
		}
	}

	// k8s Jobs are immutable. Recreate the job if the GitRepo Spec has changed.
	// Avoid deleting the job twice
	if generationChanged(gitRepo) {
		if gen, ok := job.Labels[generationLabel]; ok {
			t := fmt.Sprintf("%d", gitRepo.Generation)
			if t != gen {
				jobDeletedMessage := "job deletion triggered because of generation change"
				logger.V(1).Info(jobDeletedMessage)
				if err := r.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil && !apierrors.IsNotFound(err) {
					return err, true
				}
				return nil, true
			}
		}
	}

	// check if the job finished and was successful
	if job.Status.Succeeded == 1 {
		if job.Status.StartTime != nil && job.Status.CompletionTime != nil {
			duration := job.Status.CompletionTime.Sub(job.Status.StartTime.Time)
			gitjobDuration.Observe(gitRepo, duration.Seconds())
			gitjobDurationGauge.Set(gitRepo, duration.Seconds())

			go func() {
				time.Sleep(ShortLivedMetricsTTL)
				gitjobDurationGauge.Delete(gitRepo)
			}()
		}
		jobDeletedMessage := "job deletion triggered because job succeeded"
		logger.Info(jobDeletedMessage)
		if err := r.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil && !apierrors.IsNotFound(err) {
			return err, false
		}
		r.Recorder.Event(gitRepo, fleetevent.Normal, "JobDeleted", jobDeletedMessage)
	}

	return nil, false
}

func jobKey(g v1alpha1.GitRepo) *quartz.JobKey {
	return quartz.NewJobKey(string(g.UID))
}

// deletePollingJob deletes the polling job scheduled for the provided gitrepo, if any, and returns any error that may
// have happened in the process.
// Returns a nil error if the job could be deleted or if none existed.
func (r *GitJobReconciler) deletePollingJob(gitrepo v1alpha1.GitRepo) error {
	if r.Scheduler == nil {
		return nil
	}
	jobKey := jobKey(gitrepo)
	if _, err := r.Scheduler.GetScheduledJob(jobKey); err == nil {
		if err = r.Scheduler.DeleteJob(jobKey); err != nil {
			return fmt.Errorf("failed to delete outdated polling job: %w", err)
		}
	} else if !errors.Is(err, quartz.ErrJobNotFound) {
		return fmt.Errorf("failed to get outdated polling job for deletion: %w", err)
	}

	return nil
}

// managePollingJob creates, updates or deletes a polling job for the provided GitRepo.
func (r *GitJobReconciler) managePollingJob(logger logr.Logger, gitrepo v1alpha1.GitRepo) (bool, error) {
	jobUpdatedOrCreated := false
	if r.Scheduler == nil {
		logger.V(1).Info("Scheduler is not set; this should only happen in tests")
		return jobUpdatedOrCreated, nil
	}

	jobKey := jobKey(gitrepo)
	scheduled, err := r.Scheduler.GetScheduledJob(jobKey)

	if err != nil && !errors.Is(err, quartz.ErrJobNotFound) {
		return jobUpdatedOrCreated, fmt.Errorf("an unknown error occurred when looking for a polling job: %w", err)
	}

	if !gitrepo.Spec.DisablePolling {
		scheduledJobDescription := ""

		if err == nil {
			if detail := scheduled.JobDetail(); detail != nil {
				scheduledJobDescription = detail.Job().Description()
			}
		}

		newJob := newGitPollingJob(r.Client, r.Recorder, gitrepo, r.GitFetcher)
		currentTrigger := ctrlquartz.NewControllerTrigger(
			getPollingIntervalDuration(&gitrepo),
			gitJobPollingJitterPercent,
		)

		if errors.Is(err, quartz.ErrJobNotFound) ||
			scheduled.Trigger().Description() != currentTrigger.Description() ||
			scheduledJobDescription != newJob.Description() {
			err = r.Scheduler.ScheduleJob(
				quartz.NewJobDetailWithOptions(
					newJob,
					jobKey,
					&quartz.JobDetailOptions{
						Replace: true,
					},
				),
				currentTrigger,
			)

			if err != nil {
				return jobUpdatedOrCreated, fmt.Errorf("failed to schedule polling job: %w", err)
			}

			logger.V(1).Info("Scheduled new polling job")
			jobUpdatedOrCreated = true
		}
	} else if err == nil {
		// A job still exists, but is no longer needed; delete it.
		if err = r.Scheduler.DeleteJob(jobKey); err != nil {
			return jobUpdatedOrCreated, fmt.Errorf("failed to delete polling job: %w", err)
		}
	}

	return jobUpdatedOrCreated, nil
}

func generationChanged(r *v1alpha1.GitRepo) bool {
	// checks if generation changed.
	// it ignores the case when Status.ObservedGeneration=0 because that's
	// the initial value of a just created GitRepo and the initial value
	// for Generation in k8s is 1.
	// If we don't ignore we would be deleting the gitjob that was just created
	// until later we reconcile ObservedGeneration with Generation
	return (r.Generation != r.Status.ObservedGeneration) && r.Status.ObservedGeneration > 0
}

func getPollingIntervalDuration(gitrepo *v1alpha1.GitRepo) time.Duration {
	if gitrepo.Spec.PollingInterval == nil || gitrepo.Spec.PollingInterval.Duration == 0 {
		return defaultPollingSyncInterval
	}

	return gitrepo.Spec.PollingInterval.Duration
}

// setStatusFromGitjob sets the status fields relative to the given job in the gitRepo
func setStatusFromGitjob(ctx context.Context, c client.Client, gitRepo *v1alpha1.GitRepo, job *batchv1.Job) error {
	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(job)
	if err != nil {
		return err
	}
	uJob := &unstructured.Unstructured{Object: obj}

	result, err := status.Compute(uJob)
	if err != nil {
		return err
	}

	terminationMessage := ""
	if result.Status == status.FailedStatus {
		selector := labels.SelectorFromSet(labels.Set{"job-name": job.Name})
		podList := &corev1.PodList{}
		err := c.List(ctx, podList, &client.ListOptions{LabelSelector: selector})
		if err != nil {
			return err
		}

		sort.Slice(podList.Items, func(i, j int) bool {
			return podList.Items[i].CreationTimestamp.Before(&podList.Items[j].CreationTimestamp)
		})

		terminationMessage = result.Message
		if len(podList.Items) > 0 {
			for _, podStatus := range podList.Items[len(podList.Items)-1].Status.ContainerStatuses {
				if podStatus.Name != "step-git-source" && podStatus.State.Terminated != nil {
					terminationMessage += podStatus.State.Terminated.Message
				}
			}

			// set also the message from init containers (if they failed)
			for _, podStatus := range podList.Items[len(podList.Items)-1].Status.InitContainerStatuses {
				if podStatus.Name != "step-git-source" &&
					podStatus.State.Terminated != nil &&
					podStatus.State.Terminated.ExitCode != 0 {
					terminationMessage += podStatus.State.Terminated.Message
				}
			}
		}
	}

	gitRepo.Status.GitJobStatus = result.Status.String()

	for _, con := range result.Conditions {
		if con.Type.String() == "Ready" {
			continue
		}
		condition.Cond(con.Type.String()).SetStatus(gitRepo, string(con.Status))
		condition.Cond(con.Type.String()).SetMessageIfBlank(gitRepo, con.Message)
		condition.Cond(con.Type.String()).Reason(gitRepo, con.Reason)
	}

	// status.Compute() possible results are
	//   - InProgress
	//   - Current
	//   - Failed
	//   - Terminating
	switch result.Status {
	case status.FailedStatus:
		kstatus.SetError(gitRepo, filterFleetCLIJobOutput(terminationMessage))
	case status.CurrentStatus:
		if strings.Contains(result.Message, "Job Completed") {
			gitRepo.Status.Commit = job.Annotations["commit"]
		}
		kstatus.SetActive(gitRepo)
	case status.InProgressStatus:
		kstatus.SetTransitioning(gitRepo, "")
	case status.TerminatingStatus:
		// set active set both conditions to False
		// the job is terminating so avoid reporting errors in
		// that case
		kstatus.SetActive(gitRepo)
	}

	return nil
}

// updateErrorStatus sets the condition in the status and tries to update the resource
func updateErrorStatus(ctx context.Context, c client.Client, req types.NamespacedName, status v1alpha1.GitRepoStatus, orgErr error) error {
	reconciler.SetCondition(v1alpha1.GitRepoAcceptedCondition, &status, orgErr)

	if statusErr := updateStatus(ctx, c, req, status); statusErr != nil {
		merr := []error{orgErr, fmt.Errorf("failed to update the status: %w", statusErr)}
		return errutil.NewAggregate(merr)
	}
	return orgErr
}

// updateStatus updates the status for the GitRepo resource. It retries on
// conflict. If the status was updated successfully, it also collects (as in
// updates) metrics for the resource GitRepo resource.
func updateStatus(ctx context.Context, c client.Client, req types.NamespacedName, status v1alpha1.GitRepoStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &v1alpha1.GitRepo{}
		err := c.Get(ctx, req, t)
		if err != nil {
			return err
		}

		commit := t.Status.Commit

		// selectively update the status fields this reconciler is responsible for
		t.Status.Commit = status.Commit
		t.Status.GitJobStatus = status.GitJobStatus
		t.Status.PollingCommit = status.PollingCommit
		t.Status.LastPollingTime = status.LastPollingTime
		t.Status.ObservedGeneration = status.ObservedGeneration
		t.Status.UpdateGeneration = status.UpdateGeneration

		// only keep the Ready condition from live status, it's calculated by the status reconciler
		conds := []genericcondition.GenericCondition{}
		for _, c := range t.Status.Conditions {
			if c.Type == "Ready" {
				conds = append(conds, c)
				break
			}
		}
		for _, c := range status.Conditions {
			if c.Type == "Ready" {
				continue
			}
			conds = append(conds, c)
		}
		t.Status.Conditions = conds

		if commit != "" && status.Commit == "" {
			// we could incur in a race condition between the poller job
			// setting the Commit and the first time the reconciler runs.
			// The poller could be faster than the reconciler setting the
			// Commit and we could reset back to "" in here
			t.Status.Commit = commit
		}

		err = c.Status().Update(ctx, t)
		if err != nil {
			return err
		}

		metrics.GitRepoCollector.Collect(ctx, t)

		return nil
	})
}

func filterFleetCLIJobOutput(output string) string {
	// first split the output in lines
	lines := strings.Split(output, "\n")
	s := ""
	for _, l := range lines {
		s += getFleetCLIErrorsFromLine(l)
	}

	s = strings.Trim(s, "\n")
	// in the case that all the messages from fleet apply are from libraries
	// we just report an unknown error
	if s == "" {
		s = "Unknown error"
	}

	return s
}

func getFleetCLIErrorsFromLine(l string) string {
	type LogEntry struct {
		Level         string `json:"level"`
		FleetErrorMsg string `json:"fleetErrorMessage"`
		Time          string `json:"time"`
		Msg           string `json:"msg"`
	}
	s := ""
	open := strings.IndexByte(l, '{')
	if open == -1 {
		// line does not contain a valid json string
		return ""
	}
	close := strings.IndexByte(l, '}')
	if close != -1 {
		if close < open {
			// looks like there is some garbage before a possible json string
			// ignore everything up to that closing bracked and try again
			return getFleetCLIErrorsFromLine(l[close+1:])
		}
	} else if close == -1 {
		// line does not contain a valid json string
		return ""
	}
	var entry LogEntry
	if err := json.Unmarshal([]byte(l[open:close+1]), &entry); err == nil {
		if entry.FleetErrorMsg != "" {
			s = s + entry.FleetErrorMsg + "\n"
		}
	}
	// check if there's more to parse
	if close+1 < len(l) {
		s += getFleetCLIErrorsFromLine(l[close+1:])
	}

	return s
}

// getNextCommit returns a commit SHA coming either from the status' webhook
// commit or, with lower precedence, from the polling commit.
func getNextCommit(status v1alpha1.GitRepoStatus) string {
	commit := status.Commit
	if status.PollingCommit != "" && status.PollingCommit != commit {
		commit = status.PollingCommit
	}
	// We could be using polling but webhooks react immediately to updates.
	// Give preference to the webhook commit.
	if status.WebhookCommit != "" && status.WebhookCommit != commit {
		commit = status.WebhookCommit
	}

	return commit
}
