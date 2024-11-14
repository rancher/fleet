package reconciler

import (
	"context"
	"fmt"
	"sort"

	"github.com/rancher/fleet/internal/cmd/controller/status"
	"github.com/rancher/fleet/internal/cmd/controller/summary"
	"github.com/rancher/fleet/internal/resourcestatus"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/durations"
	"github.com/rancher/fleet/pkg/sharding"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type StatusReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Workers int
	ShardID string
}

func (r *StatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.GitRepo{}).
		Watches(
			// Fan out from bundle to gitrepo
			&fleet.Bundle{},
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, a client.Object) []ctrl.Request {
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
			builder.WithPredicates(status.BundleStatusChangedPredicate()),
		).
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

	err = r.Client.Status().Update(ctx, gitrepo)
	if err != nil {
		logger.Error(err, "Reconcile failed update to git repo status", "status", gitrepo.Status)
		return ctrl.Result{RequeueAfter: durations.GitRepoStatusDelay}, nil
	}

	return ctrl.Result{}, nil
}

func setStatus(list *fleet.BundleDeploymentList, gitrepo *fleet.GitRepo) error {
	// sort for resourceKey?
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].UID < list.Items[j].UID
	})

	err := status.SetFields(list, &gitrepo.Status.StatusBase)
	if err != nil {
		return err
	}

	resourcestatus.SetResources(list, &gitrepo.Status.StatusBase)

	summary.SetReadyConditions(&gitrepo.Status, "Bundle", gitrepo.Status.Summary)

	gitrepo.Status.Display.ReadyBundleDeployments = fmt.Sprintf("%d/%d",
		gitrepo.Status.Summary.Ready,
		gitrepo.Status.Summary.DesiredReady)

	return nil
}
