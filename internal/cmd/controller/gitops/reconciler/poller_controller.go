package reconciler

import (
	"context"
	"math/rand/v2"
	"time"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	fleetevent "github.com/rancher/fleet/pkg/event"
	"github.com/rancher/fleet/pkg/sharding"
	"github.com/rancher/wrangler/v3/pkg/condition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const requeueAlmostNow = 5 * time.Millisecond

type RealClock struct{}

func (RealClock) Now() time.Time                  { return time.Now() }
func (RealClock) Since(t time.Time) time.Duration { return time.Since(t) }

type PollerReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Workers    int
	ShardID    string
	GitFetcher GitFetcher
	Clock      TimeGetter
	Recorder   record.EventRecorder
}

func (r *PollerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.GitRepo{},
			builder.WithPredicates(
				predicate.GenerationChangedPredicate{},
			),
		).
		Named("GitRepoPoller").
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)
}

// Reconcile implements a poller that triggers at the polling interval duration configured for a GitRepo
func (r *PollerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("gitops-poller")
	gitrepo := &fleet.GitRepo{}

	if err := r.Get(ctx, req.NamespacedName, gitrepo); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	} else if errors.IsNotFound(err) {
		return ctrl.Result{}, nil
	}
	gitrepoOrig := gitrepo.DeepCopy()

	// Restrictions / Overrides, gitrepo reconciler is responsible for setting error in status
	if err := AuthorizeAndAssignDefaults(ctx, r.Client, gitrepo); err != nil {
		// the gitjob_controller will handle the error
		return ctrl.Result{}, nil
	}

	if !gitrepo.DeletionTimestamp.IsZero() {
		// the gitjob_controller will handle deletion
		return ctrl.Result{}, nil
	}

	if gitrepo.Spec.Repo == "" {
		return ctrl.Result{}, nil
	}

	logger = logger.WithValues(
		"generation", gitrepo.Generation,
		"pollerGeneration", gitrepo.Status.PollerGeneration,
		"pollerCommit", gitrepo.Status.PollerCommit,
		"pollerInterval", gitrepo.Spec.PollingInterval,
		"lastPollingTime", gitrepo.Status.LastPollingTime,
	)
	ctx = log.IntoContext(ctx, logger)

	logger.V(1).Info("Reconciling GitRepo poller")

	result := reconcile.Result{RequeueAfter: getPollingIntervalDuration(gitrepo)}
	result.RequeueAfter = addJitter(result.RequeueAfter)

	if pollerGenerationChanged(gitrepo) {
		// Generation changed.
		// We reset the poller back to the initial state.
		// We requeue with a 5ms value (short enough) so any pending
		// event in the waiting to be processed queue is updated and processed.
		// The internal queue updates a waiting event only if the new time is less
		// than the time already queued.
		// Example: we have an event waiting that should be processed at time X,
		// if we enqueue another event that should be processed at time X+Y (greater) it
		// will be ignored and the initial one would take precedence.
		// We also reset the lastPollingTime value to zero to ensure that the event right after
		// this call triggers the polling call.
		gitrepo.Status.LastPollingTime = metav1.Time{}
		result.RequeueAfter = requeueAlmostNow
		logger.V(1).Info("generation changed")
	} else {
		oldCommit := gitrepo.Status.PollerCommit
		if r.shouldRunPollingTask(gitrepo) {
			logger.V(1).Info("polling")
			gitrepo.Status.LastPollingTime.Time = r.Clock.Now()
			commit, err := r.GitFetcher.LatestCommit(ctx, gitrepo, r.Client)
			if err != nil {
				r.Recorder.Event(gitrepo, fleetevent.Warning, "FailedToCheckCommit", err.Error())
				logger.Info("Failed to check for latest commit", "error", err)
			}
			if oldCommit != commit {
				r.Recorder.Event(gitrepo, fleetevent.Normal, "GotNewCommit", commit)
				logger.Info("New commit from repository", "newCommit", commit)
			}
			condition.Cond(gitPollingCondition).SetError(&gitrepo.Status, "", err)
			gitrepo.Status.PollerCommit = commit
		}
	}

	gitrepo.Status.PollerGeneration = gitrepo.Generation

	// Patch status, either to requeue after the configured poller interval
	// or to requeue almost now because of generation changed
	statusPatch := client.MergeFrom(gitrepoOrig)
	err := r.Status().Patch(ctx, gitrepo, statusPatch)
	if err != nil {
		logger.V(1).Info("Reconcile failed patching gitrepo poller status", "status", gitrepo.Status, "error", err)
		// if patching failed we requeue immediately
		return ctrl.Result{}, err
	}

	nanos := result.RequeueAfter.Nanoseconds()
	logger.V(1).Info("return result", "requeue", nanos)

	return result, nil
}

func (r *PollerReconciler) shouldRunPollingTask(gitrepo *v1alpha1.GitRepo) bool {
	if gitrepo.Spec.DisablePolling {
		return false
	}

	t := gitrepo.Status.LastPollingTime

	if t.IsZero() || (r.Clock.Since(t.Time) >= getPollingIntervalDuration(gitrepo)) {
		return true
	}

	return false
}

func getPollingIntervalDuration(gitrepo *v1alpha1.GitRepo) time.Duration {
	if gitrepo.Spec.PollingInterval == nil || gitrepo.Spec.PollingInterval.Duration == 0 {
		return defaultPollingSyncInterval
	}

	return gitrepo.Spec.PollingInterval.Duration
}

// addJitter to the requeue time to avoid thundering herd
// generate a random number between 0% and +10% of the duration
func addJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}

	return d + time.Duration(rand.Int64N(int64(d)/10)) // nolint:gosec // gosec G404 false positive, not used for crypto
}

func pollerGenerationChanged(r *v1alpha1.GitRepo) bool {
	return r.Generation != r.Status.PollerGeneration
}
