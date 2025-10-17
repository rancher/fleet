package reconciler

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/reugn/go-quartz/quartz"

	fleetutil "github.com/rancher/fleet/internal/cmd/controller/errorutil"
	"github.com/rancher/fleet/internal/cmd/controller/finalize"
	"github.com/rancher/fleet/internal/cmd/controller/imagescan"
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
	"k8s.io/apimachinery/pkg/api/equality"
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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	defaultPollingSyncInterval = 15 * time.Second
	gitPollingCondition        = "GitPolling"
	generationLabel            = "fleet.cattle.io/gitrepo-generation"
	forceSyncGenerationLabel   = "fleet.cattle.io/force-sync-generation"
	// The TTL is the grace period for short-lived metrics to be kept alive to
	// make sure Prometheus scrapes them.
	ShortLivedMetricsTTL = 120 * time.Second
)

var (
	zero = int32(0)

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
					TypedResourceVersionUnchangedPredicate[client.Object]{},
					predicate.GenerationChangedPredicate{},
					predicate.AnnotationChangedPredicate{},
					predicate.LabelChangedPredicate{},
					webhookCommitChangedPredicate(),
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

	if !controllerutil.ContainsFinalizer(gitrepo, finalize.GitRepoFinalizer) {
		err := r.addGitRepoFinalizer(ctx, req.NamespacedName)
		if client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, err
		}

		// requeue as adding the finalizer changes the spec
		return ctrl.Result{RequeueAfter: durations.DefaultRequeueAfter}, nil
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
		return ctrl.Result{}, nil
	}

	oldCommit := gitrepo.Status.Commit
	repoPolled, err := r.repoPolled(ctx, gitrepo)
	if err != nil {
		r.Recorder.Event(gitrepo, fleetevent.Warning, "FailedToCheckCommit", err.Error())
		logger.Info("Failed to check for latest commit", "error", err)
	} else if repoPolled && oldCommit != gitrepo.Status.Commit {
		r.Recorder.Event(gitrepo, fleetevent.Normal, "GotNewCommit", gitrepo.Status.Commit)
		logger.Info("New commit from repository", "newCommit", gitrepo.Status.Commit)
	}

	// check for webhook commit
	if gitrepo.Status.WebhookCommit != "" && gitrepo.Status.WebhookCommit != gitrepo.Status.Commit {
		gitrepo.Status.Commit = gitrepo.Status.WebhookCommit
	}

	// From this point onwards we have to take into account if the poller
	// task was executed.
	// If so, we need to return a Result with EnqueueAfter set.

	res, err := r.manageGitJob(ctx, logger, gitrepo, oldCommit, repoPolled)
	if err != nil || res.RequeueAfter > 0 {
		return res, err
	}

	setAcceptedCondition(&gitrepo.Status, nil)

	err = updateStatus(ctx, r.Client, req.NamespacedName, gitrepo.Status)
	if err != nil {
		logger.Error(err, "Reconcile failed final update to git repo status", "status", gitrepo.Status)

		return r.result(gitrepo), err
	}

	return r.result(gitrepo), nil
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
func (r *GitJobReconciler) manageGitJob(ctx context.Context, logger logr.Logger, gitrepo *v1alpha1.GitRepo, oldCommit string, repoPolled bool) (reconcile.Result, error) {
	name := types.NamespacedName{Namespace: gitrepo.Namespace, Name: gitrepo.Name}
	var job batchv1.Job
	err := r.Get(ctx, types.NamespacedName{
		Namespace: gitrepo.Namespace,
		Name:      jobName(gitrepo),
	}, &job)
	if err != nil && !apierrors.IsNotFound(err) {
		err = fmt.Errorf("error retrieving git job: %w", err)
		r.Recorder.Event(gitrepo, fleetevent.Warning, "FailedToGetGitJob", err.Error())
		return r.result(gitrepo), err
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
			} else {
				if repoPolled && oldCommit != gitrepo.Status.Commit {
					r.Recorder.Event(gitrepo, fleetevent.Normal, "GotNewCommit", gitrepo.Status.Commit)
				}
			}
		}

		if r.shouldCreateJob(gitrepo, oldCommit) {
			r.updateGenerationValuesIfNeeded(gitrepo)
			if err := r.validateExternalSecretExist(ctx, gitrepo); err != nil {
				r.Recorder.Event(gitrepo, fleetevent.Warning, "FailedValidatingSecret", err.Error())
				return r.result(gitrepo), updateErrorStatus(ctx, r.Client, name, gitrepo.Status, err)
			}
			if err := r.createJobAndResources(ctx, gitrepo, logger); err != nil {
				gitjobsCreatedFailure.Inc(gitrepo)
				return r.result(gitrepo), err
			}
			gitjobsCreatedSuccess.Inc(gitrepo)
		}
	} else if gitrepo.Status.Commit != "" && gitrepo.Status.Commit == oldCommit {
		err, recreateGitJob := r.deleteJobIfNeeded(ctx, gitrepo, &job)
		if err != nil {
			return r.result(gitrepo), fmt.Errorf("error deleting git job: %w", err)
		}
		// job was deleted and we need to recreate it
		// Requeue so the reconciler creates the job again
		if recreateGitJob {
			return reconcile.Result{RequeueAfter: durations.DefaultRequeueAfter}, nil
		}
	}

	gitrepo.Status.ObservedGeneration = gitrepo.Generation

	if err = setStatusFromGitjob(ctx, r.Client, gitrepo, &job); err != nil {
		return r.result(gitrepo), updateErrorStatus(ctx, r.Client, name, gitrepo.Status, err)
	}

	return reconcile.Result{}, nil
}

func (r *GitJobReconciler) cleanupGitRepo(ctx context.Context, logger logr.Logger, gitrepo *v1alpha1.GitRepo) error {
	logger.Info("Gitrepo deleted, deleting bundle, image scans")

	metrics.GitRepoCollector.Delete(gitrepo.Name, gitrepo.Namespace)

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

func (r *GitJobReconciler) addGitRepoFinalizer(ctx context.Context, nsName types.NamespacedName) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gitrepo := &v1alpha1.GitRepo{}
		if err := r.Get(ctx, nsName, gitrepo); err != nil {
			return err
		}

		controllerutil.AddFinalizer(gitrepo, finalize.GitRepoFinalizer)

		return r.Update(ctx, gitrepo)
	})
	if err != nil {
		return client.IgnoreNotFound(err)
	}

	return nil
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

// repoPolled returns true if the git poller was executed and the repo should still be polled.
func (r *GitJobReconciler) repoPolled(ctx context.Context, gitrepo *v1alpha1.GitRepo) (bool, error) {
	if gitrepo.Spec.DisablePolling {
		return false, nil
	}
	if r.shouldRunPollingTask(gitrepo) {
		gitrepo.Status.LastPollingTime.Time = r.Clock.Now()
		commit, err := monitorLatestCommit(gitrepo, func() (string, error) {
			return r.GitFetcher.LatestCommit(ctx, gitrepo, r.Client)
		})
		condition.Cond(gitPollingCondition).SetError(&gitrepo.Status, "", err)
		if err != nil {
			return true, err
		}
		gitrepo.Status.Commit = commit

		return true, nil
	}

	return false, nil
}

func (r *GitJobReconciler) shouldRunPollingTask(gitrepo *v1alpha1.GitRepo) bool {
	if gitrepo.Spec.DisablePolling {
		return false
	}

	t := gitrepo.Status.LastPollingTime

	if t.IsZero() || (r.Clock.Since(t.Time) >= getPollingIntervalDuration(gitrepo)) {
		return true
	}
	if gitrepo.Status.ObservedGeneration != gitrepo.Generation {
		return true
	}
	return false
}

func (r *GitJobReconciler) result(gitrepo *v1alpha1.GitRepo) reconcile.Result {
	// We always return a reconcile Result with RequeueAfter set to the polling interval
	// unless polling is disabled.
	// This is done to ensure the polling cycle is never broken due to race conditions
	// between regular events and RequeueAfter events.
	// Requeuing more events when there is already an event in the queue is not a problem
	// because controller-runtime ignores events with higher timestamp
	// For example, if we have an event in the queue that should be executed at time X
	// and we try to enqueue another event that should be executed at time X+10 it will be
	// dropped.
	// If we try to enqueue an event at time X-10, it will replace the one in the queue.
	// The queue will always keep the event that should be triggered earlier.
	if gitrepo.Spec.DisablePolling {
		return reconcile.Result{}
	}

	// Calculate next reconciliation schedule based on the elapsed time since the last polling
	// so it matches the configured polling interval.
	// A fixed value may lead to drifts due to out-of-schedule reconciliations.
	requeueAfter := getPollingIntervalDuration(gitrepo) - r.Clock.Since(gitrepo.Status.LastPollingTime.Time)
	if requeueAfter <= 0 {
		// This is a protection for cases in which the calculation above is 0 or less.
		// In those cases controller-runtime does not call AddAfter for this object and
		// the RequeueAfter cycle is lost.
		// To ensure that this cycle is not broken we force the object to be requeued.
		return reconcile.Result{RequeueAfter: durations.DefaultRequeueAfter}
	}
	requeueAfter = addJitter(requeueAfter)
	return reconcile.Result{RequeueAfter: requeueAfter}
}

// addJitter to the requeue time to avoid thundering herd
// generate a random number between -10% and +10% of the duration
func addJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}

	return d + time.Duration(rand.Int64N(int64(d)/10)) // nolint:gosec // gosec G404 false positive, not used for crypto
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

// setAcceptedCondition sets the condition and updates the timestamp, if the condition changed
func setAcceptedCondition(status *v1alpha1.GitRepoStatus, err error) {
	cond := condition.Cond(v1alpha1.GitRepoAcceptedCondition)
	origStatus := status.DeepCopy()
	cond.SetError(status, "", fleetutil.IgnoreConflict(err))
	if !equality.Semantic.DeepEqual(origStatus, status) {
		cond.LastUpdated(status, time.Now().UTC().Format(time.RFC3339))
	}
}

// updateErrorStatus sets the condition in the status and tries to update the resource
func updateErrorStatus(ctx context.Context, c client.Client, req types.NamespacedName, status v1alpha1.GitRepoStatus, orgErr error) error {
	setAcceptedCondition(&status, orgErr)
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
		s = s + getFleetCLIErrorsFromLine(l)
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
		s = s + getFleetCLIErrorsFromLine(l[close+1:])
	}

	return s
}
