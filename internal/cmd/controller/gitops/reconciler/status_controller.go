package reconciler

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/rancher/fleet/internal/cmd/controller/status"
	"github.com/rancher/fleet/internal/resourcestatus"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/sharding"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// enqueueDelay is used as an artificial delay for enqueuing GitRepo status reconciliation requests
// This allows aggregating multiple consecutive Bundle update events, reducing the number of GitRepo status changes at the cost of introducing a delay in the notification
const enqueueDelay = 3 * time.Second

type StatusReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Workers int
	ShardID string
}

func (r *StatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.GitRepo{}).
		WatchesRawSource(wrapSourceWithForcedDelay(enqueueDelay, source.TypedKind(
			// Fan out from bundle to gitrepo
			mgr.GetCache(),
			&fleet.Bundle{},
			handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, a *fleet.Bundle) []ctrl.Request {
				repo := a.GetLabels()[fleet.RepoLabel]
				if repo != "" {
					return []ctrl.Request{{
						NamespacedName: types.NamespacedName{
							Namespace: a.GetNamespace(),
							Name:      repo,
						},
					}}
				}

				return []ctrl.Request{}
			}),
			sharding.TypedFilterByShardID[*fleet.Bundle](r.ShardID), // WatchesRawSources ignores event filters, we need to use a predicate
			status.BundleStatusChangedPredicate(),
		))).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Named("GitRepoStatus").
		Complete(r)
}

// Reconcile reads the stat of the GitRepo and BundleDeployments and
// computes status fields for the GitRepo. This status is used to
// display information to the user.
func (r *StatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("gitops-status")

	gitrepo := &fleet.GitRepo{}
	if err := r.Get(ctx, req.NamespacedName, gitrepo); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	} else if errors.IsNotFound(err) {
		logger.V(1).Info("Gitrepo deleted, cleaning up poll jobs")
		return ctrl.Result{}, nil
	}

	orig := gitrepo.DeepCopy()

	// Restrictions are not checked here: the gitjob reconciler reports violations
	// in the Accepted condition, which setReadyCondition takes into account.
	// Skipping the status computation on violations would leave Ready stale.

	if !gitrepo.DeletionTimestamp.IsZero() {
		// the gitjob_controller will handle deletion
		return ctrl.Result{}, nil
	}

	if gitrepo.Spec.Repo == "" {
		return ctrl.Result{}, nil
	}

	logger = logger.WithValues("generation", gitrepo.Generation, "commit", gitrepo.Status.Commit).WithValues("conditions", gitrepo.Status.Conditions)
	ctx = log.IntoContext(ctx, logger)

	logger.V(1).Info("Reconciling GitRepo status")

	bdList := &fleet.BundleDeploymentList{}
	err := r.List(ctx, bdList, client.MatchingLabels{
		fleet.RepoLabel:            gitrepo.Name,
		fleet.BundleNamespaceLabel: gitrepo.Namespace,
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	err = setStatus(bdList, gitrepo)
	if err != nil {
		return ctrl.Result{}, err
	}

	if gitrepo.Status.GitJobStatus != "Current" {
		gitrepo.Status.Display.State = "GitUpdating"
	}

	err = r.setReadyCondition(ctx, gitrepo)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Patch under an optimistic lock. A merge patch replaces the whole conditions
	// array, so without the lock a stale read here silently drops conditions owned
	// by the gitjob reconciler (Accepted in particular). That reconciler ignores
	// status-only changes, so a dropped condition is never restored.
	if err := status.PatchStatus(ctx, r.Client, orig, gitrepo); err != nil {
		if errors.IsConflict(err) {
			// Expected under the optimistic lock: another controller wrote the
			// status first. Requeuing recomputes it from a fresh read, so this
			// is not worth reporting as an error. Come back sooner than the
			// status delay, which exists to wait for a write that has by
			// definition already happened here.
			logger.V(1).Info("Conflict updating git repo status, retrying", "error", err)
			return ctrl.Result{RequeueAfter: durations.StatusConflictRequeue}, nil
		}

		logger.Error(err, "Reconcile failed update to git repo status", "status", gitrepo.Status)

		return ctrl.Result{RequeueAfter: durations.GitRepoStatusDelay}, nil
	}

	return ctrl.Result{}, nil
}

func setStatus(list *fleet.BundleDeploymentList, gitrepo *fleet.GitRepo) error {
	// sort bundledeployments so lists in status are always in the same order
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].UID < list.Items[j].UID
	})

	err := status.SetFields(list, &gitrepo.Status.StatusBase)
	if err != nil {
		return err
	}

	resourcestatus.SetResources(list.Items, &gitrepo.Status.StatusBase)

	gitrepo.Status.Display.ReadyBundleDeployments = fmt.Sprintf("%d/%d",
		gitrepo.Status.Summary.Ready,
		gitrepo.Status.Summary.DesiredReady)

	return nil
}

// setReadyCondition computes the GitRepo's Ready condition, see
// status.SetReadyCondition.
func (r StatusReconciler) setReadyCondition(ctx context.Context, gitrepo *fleet.GitRepo) error {
	notReadyBundle, err := r.notReadyBundle(ctx, gitrepo)
	if err != nil {
		return err
	}
	status.SetReadyCondition(&gitrepo.Status.StatusBase, notReadyBundle, fleet.GitRepoAcceptedCondition)

	return nil
}

// notReadyBundle returns the first bundle from the given gitrepo which is not ready, or nil. Its
// Ready condition needs to be made explicitly visible in the gitrepo status since the other
// statuses are calculated from bundle deployments, which do not exist when rendering manifests
// fail.
func (r StatusReconciler) notReadyBundle(ctx context.Context, gitrepo *fleet.GitRepo) (*fleet.Bundle, error) {
	bList := &fleet.BundleList{}
	err := r.List(ctx, bList, client.MatchingLabels{
		fleet.RepoLabel: gitrepo.Name,
	}, client.InNamespace(gitrepo.Namespace))
	if err != nil {
		return nil, err
	}

	// Make sure the bundles are always iterated in the same order
	// The code below will pick the first element matching the condition, so successive executions should produce the same result.
	sort.Slice(bList.Items, func(i, j int) bool {
		return bList.Items[i].UID < bList.Items[j].UID
	})

	for i, bundle := range bList.Items {
		for _, c := range bundle.Status.Conditions {
			if c.Type == string(fleet.Ready) && c.Status == v1.ConditionFalse {
				return &bList.Items[i], nil
			}
		}
	}

	return nil, nil
}

type forcedDelayingSource[R comparable] struct {
	source.TypedSource[R]
	delay time.Duration
}

func wrapSourceWithForcedDelay[R comparable](delay time.Duration, delegate source.TypedSource[R]) source.TypedSource[R] {
	return &forcedDelayingSource[R]{TypedSource: delegate, delay: delay}
}

func (s *forcedDelayingSource[R]) Start(ctx context.Context, delegate workqueue.TypedRateLimitingInterface[R]) error {
	return s.TypedSource.Start(ctx, &forcedDelayingQueue[R]{delegate, s.delay})
}

type forcedDelayingQueue[R comparable] struct {
	workqueue.TypedRateLimitingInterface[R]
	delay time.Duration
}

func (f *forcedDelayingQueue[R]) Add(obj R) {
	f.AddAfter(obj, f.delay)
}
