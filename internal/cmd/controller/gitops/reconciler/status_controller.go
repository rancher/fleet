package reconciler

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/rancher/fleet/internal/cmd/controller/summary"
	v1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type StatusReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Workers int
	ShardID string
}

func (r *StatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.GitRepo{}).
		Watches(
			// Fan out from bundle to gitrepo
			&v1alpha1.Bundle{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, a client.Object) []ctrl.Request {
				repo := a.GetLabels()[v1alpha1.RepoLabel]
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
			builder.WithPredicates(bundleStatusChangedPredicate()),
		).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Named("GitRepoStatus").
		Complete(r)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// The Reconcile function compares the state specified by
// the GitRepo object against the actual cluster state, and then
// performs operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *StatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("gitops-status")
	gitrepo := &v1alpha1.GitRepo{}

	if err := r.Get(ctx, req.NamespacedName, gitrepo); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	} else if errors.IsNotFound(err) {
		logger.V(1).Info("Gitrepo deleted, cleaning up poll jobs")
		return ctrl.Result{}, nil
	}

	// Restrictions / Overrides, gitrepo reconciler is responsible for setting error in status
	_, err := authorizeAndAssignDefaults(ctx, r.Client, gitrepo)
	if err != nil {
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

	logger = logger.WithValues("generation", gitrepo.Generation, "commit", gitrepo.Status.Commit).WithValues("conditions", gitrepo.Status.Conditions)
	ctx = log.IntoContext(ctx, logger)

	logger.V(1).Info("Reconciling GitRepo status")

	err = setStatus(ctx, r.Client, gitrepo)
	if err != nil {
		return ctrl.Result{}, err
	}

	if gitrepo.Status.GitJobStatus != "Current" {
		gitrepo.Status.Display.State = "GitUpdating"
	}

	err = r.Client.Status().Update(ctx, gitrepo)
	if err != nil {
		logger.Error(err, "Reconcile failed update to git repo status", "status", gitrepo.Status)
		return ctrl.Result{RequeueAfter: 1 * time.Second}, err
	}

	return ctrl.Result{}, nil
}

// bundleStatusChangedPredicate returns true if the bundle
// status has changed, or the bundle was created
func bundleStatusChangedPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			n, isBundle := e.ObjectNew.(*v1alpha1.Bundle)
			if !isBundle {
				return false
			}
			o := e.ObjectOld.(*v1alpha1.Bundle)
			if n == nil || o == nil {
				return false
			}
			return !reflect.DeepEqual(n.Status, o.Status)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return true
		},
	}
}
func setStatus(ctx context.Context, c client.Client, gitrepo *v1alpha1.GitRepo) error {
	err := setStatusFromBundleDeployments(ctx, c, gitrepo)
	if err != nil {
		return err
	}

	err = setStatusFromBundles(ctx, c, gitrepo)
	if err != nil {
		return err
	}

	setResourceKey(ctx, c, gitrepo)

	gitrepo.Status.Display.ReadyBundleDeployments = fmt.Sprintf("%d/%d",
		gitrepo.Status.Summary.Ready,
		gitrepo.Status.Summary.DesiredReady)

	return nil
}

func setStatusFromBundles(ctx context.Context, c client.Client, gitrepo *v1alpha1.GitRepo) error {
	bundles := &v1alpha1.BundleList{}
	err := c.List(ctx, bundles, client.InNamespace(gitrepo.Namespace), client.MatchingLabels{
		v1alpha1.RepoLabel: gitrepo.Name,
	})
	if err != nil {
		return err
	}

	sort.Slice(bundles.Items, func(i, j int) bool {
		return bundles.Items[i].Name < bundles.Items[j].Name
	})

	var (
		clustersDesiredReady int
		clustersReady        = -1
	)

	for _, bundle := range bundles.Items {
		if bundle.Status.Summary.DesiredReady > 0 {
			clustersDesiredReady = bundle.Status.Summary.DesiredReady
			if clustersReady < 0 || bundle.Status.Summary.Ready < clustersReady {
				clustersReady = bundle.Status.Summary.Ready
			}
		}
	}

	if clustersReady < 0 {
		clustersReady = 0
	}
	gitrepo.Status.DesiredReadyClusters = clustersDesiredReady
	gitrepo.Status.ReadyClusters = clustersReady
	summary.SetReadyConditions(&gitrepo.Status, "Bundle", gitrepo.Status.Summary)
	return nil
}

func setStatusFromBundleDeployments(ctx context.Context, c client.Client, gitrepo *v1alpha1.GitRepo) error {
	list := &v1alpha1.BundleDeploymentList{}
	err := c.List(ctx, list, client.MatchingLabels{
		v1alpha1.RepoLabel:            gitrepo.Name,
		v1alpha1.BundleNamespaceLabel: gitrepo.Namespace,
	})
	if err != nil {
		return err
	}

	gitrepo.Status.Summary = v1alpha1.BundleSummary{}

	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].UID < list.Items[j].UID
	})

	var (
		maxState v1alpha1.BundleState
		message  string
	)

	for _, bd := range list.Items {
		state := summary.GetDeploymentState(&bd)
		summary.IncrementState(&gitrepo.Status.Summary, bd.Name, state, summary.MessageFromDeployment(&bd), bd.Status.ModifiedStatus, bd.Status.NonReadyStatus)
		gitrepo.Status.Summary.DesiredReady++
		if v1alpha1.StateRank[state] > v1alpha1.StateRank[maxState] {
			maxState = state
			message = summary.MessageFromDeployment(&bd)
		}
	}

	if maxState == v1alpha1.Ready {
		maxState = ""
		message = ""
	}

	gitrepo.Status.Display.State = string(maxState)
	gitrepo.Status.Display.Message = message
	gitrepo.Status.Display.Error = len(message) > 0

	return nil
}
