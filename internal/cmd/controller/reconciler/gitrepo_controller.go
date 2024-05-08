// Copyright (c) 2021-2023 SUSE LLC

package reconciler

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	grutil "github.com/rancher/fleet/internal/cmd/controller/gitrepo"
	"github.com/rancher/fleet/internal/cmd/controller/imagescan"
	"github.com/rancher/fleet/internal/metrics"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"
	"github.com/reugn/go-quartz/quartz"

	"github.com/rancher/wrangler/v2/pkg/condition"
	"github.com/rancher/wrangler/v2/pkg/genericcondition"
	"github.com/rancher/wrangler/v2/pkg/name"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	errutil "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// GitRepoReconciler  reconciles a GitRepo object
type GitRepoReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Scheduler quartz.Scheduler
	ShardID   string

	Workers int
}

//+kubebuilder:rbac:groups=fleet.cattle.io,resources=gitrepos,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=gitrepos/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=gitrepos/finalizers,verbs=update

// Reconcile creates resources for a GitRepo
// nolint:gocyclo // creates multiple owned resources
func (r *GitRepoReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("gitrepo")

	gitrepo := &fleet.GitRepo{}
	if err := r.Get(ctx, req.NamespacedName, gitrepo); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if gitrepo.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(gitrepo, gitRepoFinalizer) {
			controllerutil.AddFinalizer(gitrepo, gitRepoFinalizer)
			if err := r.Update(ctx, gitrepo); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		if controllerutil.ContainsFinalizer(gitrepo, gitRepoFinalizer) {
			// Clean up
			logger.V(1).Info("Gitrepo deleted, deleting bundle, image scans")

			metrics.GitRepoCollector.Delete(req.NamespacedName.Name, req.NamespacedName.Namespace)

			if err := purgeBundles(ctx, r.Client, req.NamespacedName); err != nil {
				return ctrl.Result{}, err
			}

			// remove the job scheduled by imagescan, if any
			_ = r.Scheduler.DeleteJob(imagescan.GitCommitKey(req.Namespace, req.Name))

			if err := purgeImageScans(ctx, r.Client, req.NamespacedName); err != nil {
				return ctrl.Result{}, err
			}

			controllerutil.RemoveFinalizer(gitrepo, gitRepoFinalizer)
			if err := r.Update(ctx, gitrepo); err != nil {
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	logger = logger.WithValues("commit", gitrepo.Status.Commit)
	logger.V(1).Info("Reconciling GitRepo", "lastAccepted", acceptedLastUpdate(gitrepo.Status.Conditions))

	gitrepo.Status.ObservedGeneration = gitrepo.Generation

	if gitrepo.Spec.Repo == "" {
		return ctrl.Result{}, nil
	}

	// Restrictions / Overrides
	// AuthorizeAndAssignDefaults mutates GitRepo and it returns nil on error
	oldStatus := gitrepo.Status.DeepCopy()
	gitrepo, err := grutil.AuthorizeAndAssignDefaults(ctx, r.Client, gitrepo)
	if err != nil {
		return ctrl.Result{}, r.updateErrorStatus(ctx, req.NamespacedName, *oldStatus, err)
	}

	// Refresh the status
	err = grutil.SetStatusFromBundleDeployments(ctx, r.Client, gitrepo)
	if err != nil {
		return ctrl.Result{}, r.updateErrorStatus(ctx, req.NamespacedName, gitrepo.Status, err)
	}

	err = grutil.SetStatusFromBundles(ctx, r.Client, gitrepo)
	if err != nil {
		return ctrl.Result{}, r.updateErrorStatus(ctx, req.NamespacedName, gitrepo.Status, err)
	}

	// Ideally, this should be done in the git job reconciler, but setting the status from bundle deployments
	// updates the display state too.
	if err = grutil.UpdateDisplayState(gitrepo); err != nil {
		return ctrl.Result{}, r.updateErrorStatus(ctx, req.NamespacedName, gitrepo.Status, err)
	}

	grutil.SetStatusFromResourceKey(ctx, r.Client, gitrepo)

	gitrepo.Status.Display.ReadyBundleDeployments = fmt.Sprintf("%d/%d",
		gitrepo.Status.Summary.Ready,
		gitrepo.Status.Summary.DesiredReady)

	r.setCondition(&gitrepo.Status, nil)

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
	logger.V(1).Info("Creating Git job resources")

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

	return ctrl.Result{}, nil
}

// setCondition sets the condition and updates the timestamp, if the condition changed
func (r *GitRepoReconciler) setCondition(status *fleet.GitRepoStatus, err error) {
	cond := condition.Cond(fleet.GitRepoAcceptedCondition)
	origStatus := status.DeepCopy()
	cond.SetError(status, "", ignoreConflict(err))
	if !equality.Semantic.DeepEqual(origStatus, status) {
		cond.LastUpdated(status, time.Now().UTC().Format(time.RFC3339))
	}
}

// updateErrorStatus sets the condition in the status and tries to update the resource
func (r *GitRepoReconciler) updateErrorStatus(ctx context.Context, req types.NamespacedName, status fleet.GitRepoStatus, orgErr error) error {
	r.setCondition(&status, orgErr)
	if statusErr := r.updateStatus(ctx, req, status); statusErr != nil {
		merr := []error{orgErr, fmt.Errorf("failed to update the status: %w", statusErr)}
		return errutil.NewAggregate(merr)
	}
	return orgErr
}

// updateStatus updates the status for the GitRepo resource. It retries on
// conflict. If the status was updated successfully, it also collects (as in
// updates) metrics for the resource GitRepo resource.
func (r *GitRepoReconciler) updateStatus(ctx context.Context, req types.NamespacedName, status fleet.GitRepoStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		t := &fleet.GitRepo{}
		err := r.Get(ctx, req, t)
		if err != nil {
			return err
		}
		t.Status = status

		err = r.Status().Update(ctx, t)
		if err != nil {
			return err
		}

		metrics.GitRepoCollector.Collect(ctx, t)

		return nil
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *GitRepoReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Note: Maybe use mgr.GetFieldIndexer().IndexField for better performance?
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.GitRepo{},
			builder.WithPredicates(
				// do not trigger for GitRepo status changes
				predicate.Or(
					predicate.GenerationChangedPredicate{},
					predicate.AnnotationChangedPredicate{},
					predicate.LabelChangedPredicate{},
				),
			),
		).
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
			builder.WithPredicates(bundleStatusChangedPredicate()),
		).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)
}

// bundleStatusChangedPredicate returns true if the bundle
// status has changed, or the bundle was created
func bundleStatusChangedPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			n, isBundle := e.ObjectNew.(*fleet.Bundle)
			if !isBundle {
				return false
			}
			o := e.ObjectOld.(*fleet.Bundle)
			if n == nil || o == nil {
				return false
			}
			return !reflect.DeepEqual(n.Status, o.Status)
		},
	}
}

func purgeBundles(ctx context.Context, c client.Client, gitrepo types.NamespacedName) error {
	bundles := &fleet.BundleList{}
	err := c.List(ctx, bundles, client.MatchingLabels{fleet.RepoLabel: gitrepo.Name}, client.InNamespace(gitrepo.Namespace))
	if err != nil {
		return err
	}

	// At this point, access to the GitRepo is unavailable as it has been deleted and cannot be found within the cluster.
	// Nevertheless, `deleteNamespace` can be found within all bundles generated from that GitRepo. Checking any bundle to get this value would be enough.
	namespace := ""
	deleteNamespace := false
	sampleBundle := fleet.Bundle{}
	if len(bundles.Items) > 0 {
		sampleBundle = bundles.Items[0]
		deleteNamespace = sampleBundle.Spec.DeleteNamespace
		namespace = sampleBundle.Spec.TargetNamespace

		if sampleBundle.Spec.KeepResources {
			deleteNamespace = false
		}
	}

	if err = purgeNamespace(ctx, c, deleteNamespace, namespace); err != nil {
		return err
	}

	for _, bundle := range bundles.Items {
		err := c.Delete(ctx, &bundle)
		if err != nil {
			return err
		}

		nn := types.NamespacedName{Namespace: bundle.Namespace, Name: bundle.Name}
		if err = purgeBundleDeployments(ctx, c, nn); err != nil {
			return client.IgnoreNotFound(err)
		}
	}

	return nil
}

func purgeBundleDeployments(ctx context.Context, c client.Client, bundle types.NamespacedName) error {
	list := &fleet.BundleDeploymentList{}
	err := c.List(
		ctx,
		list,
		client.MatchingLabels{
			fleet.BundleLabel:          bundle.Name,
			fleet.BundleNamespaceLabel: bundle.Namespace,
		},
	)
	if err != nil {
		return err
	}
	for _, bd := range list.Items {
		if controllerutil.ContainsFinalizer(&bd, bundleDeploymentFinalizer) {
			controllerutil.RemoveFinalizer(&bd, bundleDeploymentFinalizer)
			if err := c.Update(ctx, &bd); err != nil {
				return err
			}
		}

		err := c.Delete(ctx, &bd)
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
			err := c.Delete(ctx, &image)
			if err != nil {
				return err
			}
		}

	}
	return nil
}

func purgeNamespace(ctx context.Context, c client.Client, deleteNamespace bool, ns string) error {
	if !deleteNamespace {
		return nil
	}

	if ns == "" {
		return nil
	}

	// Ignore default namespaces
	defaultNamespaces := []string{"fleet-local", "cattle-fleet-system", "fleet-default", "cattle-fleet-clusters-system", "default"}
	if slices.Contains(defaultNamespaces, ns) {
		return nil
	}

	// Ignore system namespaces
	if _, isKubeNamespace := strings.CutPrefix(ns, "kube-"); isKubeNamespace {
		return nil
	}

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}
	if err := c.Delete(ctx, namespace); err != nil {
		return err
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

func ignoreConflict(err error) error {
	if apierrors.IsConflict(err) {
		return nil
	}
	return err
}
