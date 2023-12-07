// Copyright (c) 2021-2023 SUSE LLC

package reconciler

import (
	"context"
	"fmt"

	grutil "github.com/rancher/fleet/internal/cmd/controller/gitrepo"
	"github.com/rancher/fleet/internal/cmd/controller/imagescan"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	gitjob "github.com/rancher/gitjob/pkg/apis/gitjob.cattle.io/v1"
	"github.com/reugn/go-quartz/quartz"

	"github.com/rancher/wrangler/v2/pkg/condition"
	"github.com/rancher/wrangler/v2/pkg/genericcondition"
	"github.com/rancher/wrangler/v2/pkg/name"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// GitRepoReconciler  reconciles a GitRepo object
type GitRepoReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Scheduler quartz.Scheduler
}

//+kubebuilder:rbac:groups=fleet.cattle.io,resources=gitrepos,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=gitrepos/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=gitrepos/finalizers,verbs=update

// Reconcile creates bundle deployments for a bundle
// nolint:gocyclo // creates multiple owned resources
func (r *GitRepoReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("gitrepo")

	gitrepo := &fleet.GitRepo{}
	err := r.Get(ctx, req.NamespacedName, gitrepo)
	if client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, err
	}

	// Clean up
	if apierrors.IsNotFound(err) {
		logger.V(1).Info("Gitrepo deleted, deleting bundle, image scans")
		if err := purgeBundles(ctx, r.Client, req.NamespacedName); err != nil {
			return ctrl.Result{}, err
		}

		// remove the job scheduled by imagescan, if any
		_ = r.Scheduler.DeleteJob(imagescan.GitCommitKey(req.Namespace, req.Name))

		if err := purgeImageScans(ctx, r.Client, req.NamespacedName); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	logger = logger.WithValues("commit", gitrepo.Status.Commit)
	logger.V(1).Info("Reconciling GitRepo, clean up and create gitjob", "lastAccepted", acceptedLastUpdate(gitrepo.Status.Conditions))

	// Start building a gitjob
	gitrepo.Status.ObservedGeneration = gitrepo.Generation

	if gitrepo.Spec.Repo == "" {
		return ctrl.Result{}, nil
	}

	// Restrictions / Overrides
	// AuthorizeAndAssignDefaults mutates GitRepo and it returns nil on error
	oldStatus := gitrepo.Status.DeepCopy()
	gitrepo, err = grutil.AuthorizeAndAssignDefaults(ctx, r.Client, gitrepo)
	if err != nil {
		return ctrl.Result{}, r.updateErrorStatus(ctx, req.NamespacedName, *oldStatus, err)
	}

	// Refresh the status
	if gitrepo.DeletionTimestamp != nil {
		err = grutil.SetStatusFromBundleDeployments(ctx, r.Client, gitrepo)
		if err != nil {
			return ctrl.Result{}, r.updateErrorStatus(ctx, req.NamespacedName, gitrepo.Status, err)
		}

		err = grutil.SetStatusFromBundles(ctx, r.Client, gitrepo)
		if err != nil {
			return ctrl.Result{}, r.updateErrorStatus(ctx, req.NamespacedName, gitrepo.Status, err)
		}
	}

	err = grutil.SetStatusFromGitJob(ctx, r.Client, gitrepo)
	if err != nil {
		return ctrl.Result{}, r.updateErrorStatus(ctx, req.NamespacedName, gitrepo.Status, err)
	}

	grutil.SetStatusFromResourceKey(ctx, r.Client, gitrepo)

	gitrepo.Status.Display.ReadyBundleDeployments = fmt.Sprintf("%d/%d",
		gitrepo.Status.Summary.Ready,
		gitrepo.Status.Summary.DesiredReady)

	setCondition(&gitrepo.Status, nil)

	err = r.updateStatus(ctx, req.NamespacedName, gitrepo.Status)
	if err != nil {
		logger.V(1).Error(err, "Reconcile failed final update to git repo status", "status", gitrepo.Status)
		return ctrl.Result{}, err
	}

	// Validate external secrets exist
	if gitrepo.Spec.HelmSecretNameForPaths != "" {
		if err := r.Get(ctx, types.NamespacedName{Namespace: gitrepo.Namespace, Name: gitrepo.Spec.HelmSecretNameForPaths}, &corev1.Secret{}); err != nil {
			err = fmt.Errorf("failed to look up HelmSecretNameForPaths, error: %v", err)
			return ctrl.Result{}, r.updateErrorStatus(ctx, req.NamespacedName, gitrepo.Status, err)

		}
	} else if gitrepo.Spec.HelmSecretName != "" {
		if err := r.Get(ctx, types.NamespacedName{Namespace: gitrepo.Namespace, Name: gitrepo.Spec.HelmSecretName}, &corev1.Secret{}); err != nil {
			err = fmt.Errorf("failed to look up helmSecretName, error: %v", err)
			return ctrl.Result{}, r.updateErrorStatus(ctx, req.NamespacedName, gitrepo.Status, err)
		}
	}

	// Start creating/updating the job
	logger.V(1).Info("Creating GitJob resources")

	configMap, err := grutil.NewTargetsConfigMap(gitrepo)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := controllerutil.SetControllerReference(gitrepo, configMap, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	data := configMap.BinaryData
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, configMap, func() error {
		configMap.BinaryData = data
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	// No update needed, values are the same. So we ignore AlreadyExists.
	saName := name.SafeConcatName("git", gitrepo.Name)
	sa := grutil.NewServiceAccount(gitrepo.Namespace, saName)
	if err := controllerutil.SetControllerReference(gitrepo, sa, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, sa); err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, err
	}

	role := grutil.NewRole(gitrepo.Namespace, saName)
	if err := controllerutil.SetControllerReference(gitrepo, role, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, role); err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, err
	}

	rb := grutil.NewRoleBinding(gitrepo.Namespace, saName)
	if err := controllerutil.SetControllerReference(gitrepo, rb, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, rb); err != nil && !apierrors.IsAlreadyExists(err) {
		return ctrl.Result{}, err
	}

	gitjob := grutil.NewGitJob(ctx, r.Client, gitrepo, saName, configMap.Name)
	if err := controllerutil.SetControllerReference(gitrepo, gitjob, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, gitjob, grutil.MutateGitJob(gitjob)); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// updateErrorStatus sets the condition in the status and tries to update the resource
func (r *GitRepoReconciler) updateErrorStatus(ctx context.Context, req types.NamespacedName, status fleet.GitRepoStatus, orgErr error) error {
	setCondition(&status, orgErr)
	if statusErr := r.updateStatus(ctx, req, status); statusErr != nil {
		merr := []error{orgErr, fmt.Errorf("failed to update the status: %w", statusErr)}
		return errutil.NewAggregate(merr)
	}
	return orgErr
}

func (r *GitRepoReconciler) updateStatus(ctx context.Context, req types.NamespacedName, status fleet.GitRepoStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.GitRepo{}
		err := r.Get(ctx, req, t)
		if err != nil {
			return err
		}
		t.Status = status
		return r.Status().Update(ctx, t)
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *GitRepoReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Note: Maybe use mgr.GetFieldIndexer().IndexField for better performance?
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.GitRepo{}).
		Owns(&gitjob.GitJob{}).
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
		).
		WithEventFilter(
			// do not trigger for status changes
			predicate.Or(
				predicate.GenerationChangedPredicate{},
				predicate.AnnotationChangedPredicate{},
				predicate.LabelChangedPredicate{},
			),
		).
		Complete(r)
}

func purgeBundles(ctx context.Context, c client.Client, gitrepo types.NamespacedName) error {
	bundles := &fleet.BundleList{}
	err := c.List(ctx, bundles, client.MatchingLabels{fleet.RepoLabel: gitrepo.Name}, client.InNamespace(gitrepo.Namespace))
	if err != nil {
		return err
	}

	for _, bundle := range bundles.Items {
		err := c.Delete(ctx, &bundle) // nolint:gosec // does not store pointer
		if err != nil {
			return err
		}

		err = purgeBundleDeployments(ctx, c, types.NamespacedName{Namespace: bundle.Namespace, Name: bundle.Name})
		if err != nil {
			return err
		}
	}

	return nil
}

func purgeBundleDeployments(ctx context.Context, c client.Client, bundle types.NamespacedName) error {
	list := &fleet.BundleDeploymentList{}
	err := c.List(ctx, list, client.MatchingLabels{fleet.BundleLabel: bundle.Name, fleet.BundleNamespaceLabel: bundle.Namespace})
	if err != nil {
		return err
	}
	for _, bd := range list.Items {
		err := c.Delete(ctx, &bd) // nolint:gosec // does not store pointer
		if err != nil {
			return err
		}
	}

	return nil
}

func purgeImageScans(ctx context.Context, c client.Client, gitrepo types.NamespacedName) error {
	images := &fleet.ImageScanList{}
	err := c.List(ctx, images, client.InNamespace(gitrepo.Namespace))
	if err != nil {
		return err
	}

	for _, image := range images.Items {
		if image.Spec.GitRepoName == gitrepo.Name {
			err := c.Delete(ctx, &image) // nolint:gosec // does not store pointer
			if err != nil {
				return err
			}
		}

	}
	return nil
}

func acceptedLastUpdate(conds []genericcondition.GenericCondition) string {
	for _, cond := range conds {
		if cond.Type == "Accepted" {
			return cond.LastUpdateTime
		}
	}

	return ""
}

// setCondition sets the condition and updates the timestamp, if the condition changed
func setCondition(status *fleet.GitRepoStatus, err error) {
	cond := condition.Cond(fleet.GitRepoAcceptedCondition)
	cond.SetError(status, "", ignoreConflict(err))
}

func ignoreConflict(err error) error {
	if apierrors.IsConflict(err) {
		return nil
	}
	return err
}
