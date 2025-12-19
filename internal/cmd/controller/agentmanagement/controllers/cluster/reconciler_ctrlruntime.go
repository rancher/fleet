package cluster

import (
	"context"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlhandler "sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/rancher/fleet/internal/cmd/controller/agentmanagement/controllers/manageagent"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

// ClusterReconciler reconciles a Cluster object
type ClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=fleet.cattle.io,resources=clusters,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=fleet.cattle.io,resources=clusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete

// Reconcile handles Cluster resource changes
func (r *ClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var cluster fleet.Cluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			// Cluster deleted: remove cluster namespace
			logger.Info("Cluster deleted, cleaning up namespace", "cluster", req.NamespacedName)
			nsName := clusterNamespace(req.Namespace, req.Name)
			var ns corev1.Namespace
			if err := r.Get(ctx, client.ObjectKey{Name: nsName}, &ns); err == nil {
				if err := r.Delete(ctx, &ns); err != nil && !apierrors.IsNotFound(err) {
					logger.Error(err, "Failed to delete cluster namespace", "namespace", nsName)
					return ctrl.Result{}, err
				}
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if manageagent.SkipCluster(&cluster) {
		return ctrl.Result{}, nil
	}

	// Set status.Namespace if not set
	needsStatusUpdate := false
	if cluster.Status.Namespace == "" {
		cluster.Status.Namespace = clusterNamespace(cluster.Namespace, cluster.Name)
		needsStatusUpdate = true
	}

	// Ensure namespace exists
	nsName := cluster.Status.Namespace
	var ns corev1.Namespace
	if err := r.Get(ctx, client.ObjectKey{Name: nsName}, &ns); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("Creating cluster namespace", "namespace", nsName, "cluster", req.NamespacedName)
			ns = corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: nsName,
					Labels: map[string]string{
						fleet.ManagedLabel: "true",
					},
					Annotations: map[string]string{
						fleet.ClusterNamespaceAnnotation: cluster.Namespace,
						fleet.ClusterAnnotation:          cluster.Name,
					},
				},
			}
			if err := r.Create(ctx, &ns); err != nil && !apierrors.IsAlreadyExists(err) {
				logger.Error(err, "Failed to create cluster namespace", "namespace", nsName)
				return ctrl.Result{}, err
			}
		} else {
			return ctrl.Result{}, err
		}
	}

	if needsStatusUpdate {
		if err := r.Status().Update(ctx, &cluster); err != nil {
			logger.Error(err, "Failed to update cluster status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager
func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("cluster-status").
		For(&fleet.Cluster{}).
		Watches(
			&fleet.BundleDeployment{},
			ctrlhandler.EnqueueRequestsFromMapFunc(r.findClustersForBundleDeployment),
		).
		Complete(r)
}

// findClustersForBundleDeployment maps BundleDeployment changes to Cluster reconcile requests
func (r *ClusterReconciler) findClustersForBundleDeployment(ctx context.Context, obj client.Object) []reconcile.Request {
	bd, ok := obj.(*fleet.BundleDeployment)
	if !ok {
		return nil
	}

	// Get the namespace to find cluster annotations
	var ns corev1.Namespace
	if err := r.Get(ctx, client.ObjectKey{Name: bd.Namespace}, &ns); err != nil {
		return nil
	}

	clusterNS := ns.Annotations[fleet.ClusterNamespaceAnnotation]
	clusterName := ns.Annotations[fleet.ClusterAnnotation]
	if clusterNS == "" || clusterName == "" {
		return nil
	}

	logrus.Debugf("Enqueueing cluster %s/%s for bundledeployment %s/%s", clusterNS, clusterName, bd.Namespace, bd.Name)
	return []reconcile.Request{
		{
			NamespacedName: client.ObjectKey{
				Namespace: clusterNS,
				Name:      clusterName,
			},
		},
	}
}
