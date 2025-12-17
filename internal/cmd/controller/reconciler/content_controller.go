package reconciler

import (
	"context"

	"github.com/rancher/fleet/internal/config"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	"github.com/rancher/fleet/pkg/sharding"

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

// ContentReconciler reconciles a Content object
type ContentReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	ShardID string

	Workers int
}

//+kubebuilder:rbac:groups=fleet.cattle.io,resources=contents,verbs=get;list;watch;update;patch;delete
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=contents/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=fleet.cattle.io,resources=bundledeployments,verbs=get;list;watch

// SetupWithManager sets up the controller with the Manager.
func (r *ContentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&fleet.Content{}, // using For with CreateFunc only to reconcile Contents to check for finalizers
			builder.WithPredicates(
				predicate.Funcs{
					CreateFunc:  func(e event.CreateEvent) bool { return true },
					UpdateFunc:  func(e event.UpdateEvent) bool { return false },
					DeleteFunc:  func(e event.DeleteEvent) bool { return false },
					GenericFunc: func(e event.GenericEvent) bool { return false },
				},
			)).
		Watches(
			&fleet.BundleDeployment{},
			handler.EnqueueRequestsFromMapFunc(r.mapBundleDeploymentToContent),
			builder.WithPredicates(
				// Only trigger for BundleDeployment changes that affect Content references
				predicate.Funcs{
					CreateFunc: func(e event.CreateEvent) bool {
						return true
					},
					UpdateFunc: func(e event.UpdateEvent) bool {
						newBD := e.ObjectNew.(*fleet.BundleDeployment)
						oldBD := e.ObjectOld.(*fleet.BundleDeployment)

						// Reconcile if ContentNameLabel changes
						contentNameChanged := (newBD.Labels != nil && newBD.Labels[fleet.ContentNameLabel] != "") &&
							(oldBD.Labels == nil || newBD.Labels[fleet.ContentNameLabel] != oldBD.Labels[fleet.ContentNameLabel])

						return contentNameChanged
					},
					DeleteFunc: func(e event.DeleteEvent) bool {
						return true
					},
					GenericFunc: func(e event.GenericEvent) bool { return false },
				},
			),
		).
		WithEventFilter(sharding.FilterByShardID(r.ShardID)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.Workers}).
		Complete(r)
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ContentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("content")
	ctx = log.IntoContext(ctx, logger)

	content := &fleet.Content{}
	if err := r.Get(ctx, req.NamespacedName, content); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	finalizersDeleted, err := removeFinalizers(ctx, r.Client, content)
	if err != nil {
		return ctrl.Result{}, err
	}

	// List all BundleDeployments that reference this Content resource
	bdList := &fleet.BundleDeploymentList{}
	err = r.List(ctx, bdList, client.MatchingFields{config.ContentNameIndex: content.Name})
	if err != nil {
		logger.Error(err, "Failed to list BundleDeployments for Content resource")
		return ctrl.Result{}, err
	}

	newReferenceCount := 0
	for _, bd := range bdList.Items {
		// Only count non-deleted BundleDeployments
		if bd.DeletionTimestamp.IsZero() {
			newReferenceCount++
		}
	}

	// If the Content resource has no more references... delete it
	if newReferenceCount == 0 && (content.Status.ReferenceCount > 0 || finalizersDeleted) {
		logger.V(1).Info("Content resource has no more references, deleting it")
		return ctrl.Result{}, r.Delete(ctx, content)
	}

	if content.Status.ReferenceCount != newReferenceCount {
		logger.V(1).Info("Updating Content reference count", "oldCount", content.Status.ReferenceCount, "newCount", newReferenceCount)
		orig := content.DeepCopy()
		content.Status.ReferenceCount = newReferenceCount
		if err := r.Status().Patch(ctx, content, client.MergeFrom(orig)); err != nil {
			logger.Error(err, "Failed to update Content reference count status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// mapBundleDeploymentToContent maps a BundleDeployment to its associated Content resource.
func (r *ContentReconciler) mapBundleDeploymentToContent(ctx context.Context, obj client.Object) []ctrl.Request {
	bd, ok := obj.(*fleet.BundleDeployment)
	if !ok {
		return nil
	}

	contentName := bd.Labels[fleet.ContentNameLabel]
	if contentName == "" {
		return nil
	}

	return []ctrl.Request{
		{
			NamespacedName: types.NamespacedName{
				Name: contentName,
				// Content resources are cluster-scoped, so namespace is empty
			},
		},
	}
}

// removeFinalizers removes all finalizers from the given object if any exist and returns true if
// finalizers were removed, false otherwise.
func removeFinalizers(ctx context.Context, c client.Client, obj client.Object) (bool, error) {
	finalizers := obj.GetFinalizers()
	if len(finalizers) == 0 {
		// Nothing to do
		return false, nil
	}

	// Clear finalizers
	obj.SetFinalizers([]string{})

	// Update the object
	if err := c.Update(ctx, obj); err != nil {
		return false, err
	}

	return true, nil
}
