// Copyright (c) 2021-2023 SUSE LLC

package reconciler

import (
	"context"

	"github.com/reugn/go-quartz/quartz"

	"github.com/rancher/fleet/internal/cmd/controller/imagescan"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// ImageScanReconciler reconciles a ImageScan object
type ImageScanReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Scheduler quartz.Scheduler
	ShardID   string

	Workers int
}

// SetupWithManager sets up the controller with the Manager.
func (r *ImageScanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.ImageScan{}).
		WithEventFilter(
			// we do not trigger for status changes
			predicate.And(
				sharding.FilterByShardID(r.ShardID),
				predicate.Or(
					// Note: These predicates prevent cache
					// syncPeriod from triggering reconcile, since
					// cache sync is an Update event.
					predicate.GenerationChangedPredicate{},
					predicate.AnnotationChangedPredicate{},
					predicate.LabelChangedPredicate{},
				),
			)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)
}

//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=clusters/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ImageScanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("imagescan")

	image := &fleet.ImageScan{}
	err := r.Get(ctx, req.NamespacedName, image)
	if apierrors.IsNotFound(err) {
		logger.V(4).Info("Deleting ImageScan jobs")
		_ = r.Scheduler.DeleteJob(imagescan.TagScanKey(req.Namespace, req.Name))
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	logger.V(1).Info("Reconciling imagescan, clean up and schedule jobs")

	if image.Spec.Suspend {
		_ = r.Scheduler.DeleteJob(imagescan.TagScanKey(req.Namespace, req.Name))
		return ctrl.Result{}, nil
	}

	interval := image.Spec.Interval
	if interval.Seconds() == 0.0 {
		interval = imagescan.DefaultInterval
	}

	// Make sure no duplicate jobs are scheduled. DeleteJob might return an
	// error if the job does not exist, which we ignore.
	tagScanKey := imagescan.TagScanKey(req.Namespace, req.Name)
	_ = r.Scheduler.DeleteJob(tagScanKey)

	err = r.Scheduler.ScheduleJob(
		quartz.NewJobDetail(
			imagescan.NewTagScanJob(r.Client, req.Namespace, req.Name),
			tagScanKey),
		quartz.NewSimpleTrigger(interval.Duration),
	)
	if err != nil {
		logger.Error(err, "Failed to schedule imagescan tagscan job")
		return ctrl.Result{}, err
	}

	gitrepo := &fleet.GitRepo{}
	err = r.Get(ctx, client.ObjectKey{Namespace: image.Namespace, Name: image.Spec.GitRepoName}, gitrepo)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	gitCommitKey := imagescan.GitCommitKey(gitrepo.Namespace, gitrepo.Name)
	_ = r.Scheduler.DeleteJob(gitCommitKey)
	err = r.Scheduler.ScheduleJob(
		quartz.NewJobDetail(
			imagescan.NewGitCommitJob(r.Client, gitrepo.Namespace, gitrepo.Name),
			gitCommitKey),
		quartz.NewSimpleTrigger(interval.Duration),
	)
	if err != nil {
		logger.Error(err, "Failed to schedule gitrepo gitcommit job")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}
